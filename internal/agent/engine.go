package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/touchmeangel/rox_models_go/coordinator"
	"github.com/touchmeangel/rox_models_go/run"
	"github.com/touchmeangel/rox_models_go/worker"
	taskpb "github.com/touchmeangel/rox_proto/rox/task/v1"
)

type Options struct {
	RunID         string
	WorkspaceName string
}

type Result struct {
	Summary      json.RawMessage
	Workers      []WorkerResult
	UsageSummary *UsageSummary
}

type WorkerResult struct {
	MissionID     string
	Contract      string
	Vulnerability string
	ResultsRaw    json.RawMessage
	ReadErrMsg    string
	Err           error
}

type missionSummary struct {
	ID            string `json:"id"`
	Contract      string `json:"contract"`
	Vulnerability string `json:"vulnerability"`
}

type coordinatorOutput struct {
	Missions []missionSummary `json:"missions"`
}

func parseMissions(data []byte) ([]missionSummary, error) {
	var out coordinatorOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parsing coordinator results: %w", err)
	}
	return out.Missions, nil
}

type rawMissionsDoc struct {
	Missions []json.RawMessage `json:"missions"`
}

func indexMissionsByID(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var doc rawMissionsDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parsing coordinator missions payload: %w", err)
	}
	index := make(map[string]json.RawMessage, len(doc.Missions))
	for _, m := range doc.Missions {
		var idOnly struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(m, &idOnly); err != nil || idOnly.ID == "" {
			continue
		}
		index[idOnly.ID] = m
	}
	return index, nil
}

type aggregatedWorkerEntry struct {
	MissionID     string          `json:"mission_id"`
	Contract      string          `json:"contract"`
	Vulnerability string          `json:"vulnerability"`
	Success       bool            `json:"success"`
	Error         string          `json:"error,omitempty"`
	Results       json.RawMessage `json:"results,omitempty"`
}

type runSummary struct {
	RunID       string                  `json:"run_id"`
	GeneratedAt string                  `json:"generated_at"`
	Error       string                  `json:"error,omitempty"`
	Coordinator json.RawMessage         `json:"coordinator,omitempty"`
	Workers     []aggregatedWorkerEntry `json:"workers"`
	Usage       *UsageSummary           `json:"usage_summary,omitempty"`
}

func buildRunSummary(runID string, coordinatorRaw json.RawMessage, workers []WorkerResult, runErr error) (json.RawMessage, *UsageSummary, error) {
	summary := runSummary{
		RunID:       runID,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}
	if runErr != nil {
		summary.Error = runErr.Error()
	}

	usage := &UsageSummary{ByModel: map[string]*ModelUsageEntry{}}

	if len(coordinatorRaw) > 0 {
		summary.Coordinator = coordinatorRaw
		mergeUsageFrom(usage, coordinatorRaw)
	}

	for _, w := range workers {
		entry := aggregatedWorkerEntry{
			MissionID:     w.MissionID,
			Contract:      w.Contract,
			Vulnerability: w.Vulnerability,
			Success:       w.Err == nil,
		}
		switch {
		case w.Err != nil:
			entry.Error = w.Err.Error()
		case w.ReadErrMsg != "":
			entry.Error = w.ReadErrMsg
		}
		if len(w.ResultsRaw) > 0 {
			entry.Results = w.ResultsRaw
			mergeUsageFrom(usage, w.ResultsRaw)
		}
		summary.Workers = append(summary.Workers, entry)
	}

	if len(usage.ByModel) > 0 {
		finalizeUsage(usage)
		summary.Usage = usage
	} else {
		usage = nil
	}

	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("marshaling run summary: %w", err)
	}
	return data, usage, nil
}

type Engine struct {
	client           taskpb.TaskServiceClient
	runStore         *run.RunStore
	workerStore      *worker.WorkerStore
	coordinatorStore *coordinator.CoordinatorStore
	logger           *slog.Logger
}

func NewEngine(client taskpb.TaskServiceClient, runStore *run.RunStore, workerStore *worker.WorkerStore, coordinatorStore *coordinator.CoordinatorStore, logger *slog.Logger) (*Engine, error) {
	if logger == nil {
		logger = slog.Default()
	}
	return &Engine{
		client: client, runStore: runStore, workerStore: workerStore,
		coordinatorStore: coordinatorStore, logger: logger,
	}, nil
}

func (e *Engine) Execute(ctx context.Context, opts Options) (result *Result, resultErr error) {
	runID := opts.RunID
	log := e.logger.With("run_id", runID)

	var (
		coordinatorRaw json.RawMessage
		workers        []WorkerResult
	)

	log.Info("run started", "workspace", opts.WorkspaceName)

	defer func() {
		summaryData, usage, sumErr := buildRunSummary(runID, coordinatorRaw, workers, resultErr)
		if result == nil {
			result = &Result{}
		}
		if sumErr != nil {
			log.Error("failed to build run summary", "error", sumErr)
			if resultErr == nil {
				resultErr = fmt.Errorf("building run summary: %w", sumErr)
			}
			return
		}
		result.Summary = summaryData
		result.UsageSummary = usage

		if resultErr != nil {
			log.Error("run finished with error", "error", resultErr)
		} else {
			log.Info("run finished")
		}
	}()

	if _, err := e.runStore.UpdateStatus(ctx, runID, run.StatusRunningCoordinator); err != nil {
		return nil, fmt.Errorf("update run status to running_coordinator: %w", err)
	}

	coord, err := e.coordinatorStore.CreateCoordinator(ctx, runID)
	if err != nil {
		log.Error("failed to create coordinator row", "error", err)
		return nil, fmt.Errorf("create coordinator: %w", err)
	}
	clog := log.With("coordinator_id", coord.ID)

	if _, err := e.coordinatorStore.UpdateActive(ctx, coord.ID, true); err != nil {
		clog.Error("failed to mark coordinator active", "error", err)
		return nil, fmt.Errorf("mark coordinator active: %w", err)
	}

	coordResp, err := e.client.RunCoordinator(ctx, &taskpb.RunCoordinatorRequest{RunId: runID})
	if err != nil {
		clog.Error("coordinator task failed", "error", err)
		if _, cErr := e.coordinatorStore.UpdateCompleted(ctx, coord.ID, err.Error(), true); cErr != nil {
			clog.Error("failed to record coordinator completion", "error", cErr)
		}
		return nil, fmt.Errorf("coordinator task failed: %w", err)
	}
	coordinatorRaw = coordResp.GetOutput()

	if coordResp.GetError() != "" {
		clog.Error("coordinator reported error", "error", coordResp.GetError(), "retriable", coordResp.GetRetriable())
		if _, cErr := e.coordinatorStore.UpdateCompleted(ctx, coord.ID, coordResp.GetError(), coordResp.GetRetriable()); cErr != nil {
			clog.Error("failed to record coordinator completion", "error", cErr)
		}
		return nil, fmt.Errorf("coordinator task reported error: %s", coordResp.GetError())
	}

	if _, cErr := e.coordinatorStore.UpdateCompleted(ctx, coord.ID, "", false); cErr != nil {
		clog.Error("failed to record coordinator completion", "error", cErr)
	}
	clog.Info("coordinator completed")

	missions, err := parseMissions(coordResp.GetOutput())
	if err != nil {
		clog.Error("failed to parse coordinator missions", "error", err)
		return nil, err
	}
	missionIndex, err := indexMissionsByID(coordResp.GetOutput())
	if err != nil {
		clog.Error("failed to index coordinator missions", "error", err)
		return nil, err
	}

	if _, err := e.runStore.UpdateStatus(ctx, runID, run.StatusRunningWorkers); err != nil {
		return nil, fmt.Errorf("update run status to running_workers: %w", err)
	}
	log.Info("dispatching workers", "mission_count", len(missions))
	workers = e.runWorkers(ctx, runID, missionIndex, missions)

	result = &Result{Workers: workers}
	return result, nil
}

func (e *Engine) runWorkers(ctx context.Context, runID string, missionIndex map[string]json.RawMessage, missions []missionSummary) []WorkerResult {
	if len(missions) == 0 {
		return nil
	}

	results := make([]WorkerResult, len(missions))
	var wg sync.WaitGroup

	for i, m := range missions {
		mlog := e.logger.With("run_id", runID, "mission_id", m.ID)

		w, err := e.workerStore.CreateWorker(ctx, runID, m.ID)
		if err != nil {
			mlog.Error("failed to create worker row in DB", "error", err)
			results[i] = WorkerResult{
				MissionID: m.ID, Contract: m.Contract, Vulnerability: m.Vulnerability,
				Err: fmt.Errorf("failed to create worker row in DB: %w", err),
			}
			continue
		}

		wg.Add(1)
		go func(i int, m missionSummary, workerID string) {
			defer wg.Done()
			wlog := mlog.With("worker_id", workerID)

			if ok, err := e.workerStore.UpdateActive(ctx, workerID, true); err != nil {
				wlog.Error("failed to mark worker active in DB", "error", err)
				if _, cErr := e.workerStore.UpdateCompleted(ctx, workerID, err.Error(), true); cErr != nil {
					wlog.Error("failed to record worker completion after activation failure", "error", cErr)
				}
				results[i] = WorkerResult{
					MissionID: m.ID, Contract: m.Contract, Vulnerability: m.Vulnerability,
					Err: fmt.Errorf("failed to mark worker active in DB: %w", err),
				}
				return
			} else if !ok {
				err := fmt.Errorf("failed to mark worker active in DB: worker %s not found", workerID)
				wlog.Error(err.Error())
				results[i] = WorkerResult{
					MissionID: m.ID, Contract: m.Contract, Vulnerability: m.Vulnerability,
					Err: err,
				}
				return
			}

			missionRaw, ok := missionIndex[m.ID]
			if !ok {
				err := fmt.Errorf("mission %q not found in coordinator output", m.ID)
				wlog.Error("mission missing from coordinator output")
				if _, cErr := e.workerStore.UpdateCompleted(ctx, workerID, err.Error(), false); cErr != nil {
					wlog.Error("failed to record worker completion", "error", cErr)
				}
				results[i] = WorkerResult{
					MissionID: m.ID, Contract: m.Contract, Vulnerability: m.Vulnerability,
					Err: err,
				}
				return
			}

			resp, err := e.client.RunWorker(ctx, &taskpb.RunWorkerRequest{
				RunId: runID, MissionId: m.ID, Mission: missionRaw,
			})
			if err != nil {
				wlog.Error("worker task failed", "error", err)
				if _, cErr := e.workerStore.UpdateCompleted(ctx, workerID, err.Error(), true); cErr != nil {
					wlog.Error("failed to record worker completion", "error", cErr)
				}
				results[i] = WorkerResult{
					MissionID: m.ID, Contract: m.Contract, Vulnerability: m.Vulnerability,
					Err: fmt.Errorf("worker task failed: %w", err),
				}
				return
			}

			if _, cErr := e.workerStore.UpdateCompleted(ctx, workerID, resp.GetError(), resp.GetRetriable()); cErr != nil {
				wlog.Error("failed to record worker completion", "error", cErr)
			}
			if resp.GetError() != "" {
				wlog.Warn("worker completed with error", "error", resp.GetError(), "retriable", resp.GetRetriable())
			}

			results[i] = WorkerResult{
				MissionID: m.ID, Contract: m.Contract, Vulnerability: m.Vulnerability,
				ResultsRaw: resp.GetOutput(), ReadErrMsg: resp.GetError(),
			}
		}(i, m, w.ID)
	}

	wg.Wait()
	return results
}

type ModelUsageEntry struct {
	Model        string  `json:"model"`
	Calls        int     `json:"calls"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	TotalTokens  int     `json:"total_tokens"`
	PctOfCalls   float64 `json:"pct_of_calls"`
	PctOfTokens  float64 `json:"pct_of_tokens"`
}

type UsageSummary struct {
	TotalCalls  int                         `json:"total_calls"`
	TotalTokens int                         `json:"total_tokens"`
	ByModel     map[string]*ModelUsageEntry `json:"by_model"`
}

func mergeUsageFrom(dst *UsageSummary, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var wrapper struct {
		UsageSummary *UsageSummary `json:"usage_summary"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil || wrapper.UsageSummary == nil {
		return
	}
	for key, entry := range wrapper.UsageSummary.ByModel {
		existing, ok := dst.ByModel[key]
		if !ok {
			existing = &ModelUsageEntry{Model: entry.Model}
			dst.ByModel[key] = existing
		}
		existing.Calls += entry.Calls
		existing.InputTokens += entry.InputTokens
		existing.OutputTokens += entry.OutputTokens
	}
}

func finalizeUsage(u *UsageSummary) {
	totalCalls, totalTokens := 0, 0
	for _, e := range u.ByModel {
		e.TotalTokens = e.InputTokens + e.OutputTokens
		totalCalls += e.Calls
		totalTokens += e.TotalTokens
	}
	u.TotalCalls = totalCalls
	u.TotalTokens = totalTokens
	for _, e := range u.ByModel {
		if totalCalls > 0 {
			e.PctOfCalls = math.Round(100*float64(e.Calls)/float64(totalCalls)*100) / 100
		}
		if totalTokens > 0 {
			e.PctOfTokens = math.Round(100*float64(e.TotalTokens)/float64(totalTokens)*100) / 100
		}
	}
}
