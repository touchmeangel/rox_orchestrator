package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/touchmeangel/rox_orchestrator/rabbitclient"
)

type coordinatorTask struct {
	RunID     string `json:"run_id"`
	SkipBuild bool   `json:"skip_build"`
}

type workerTask struct {
	RunID     string          `json:"run_id"`
	MissionID string          `json:"mission_id"`
	Mission   json.RawMessage `json:"mission"`
}

type containerResult struct {
	RunID    string          `json:"run_id"`
	ExitCode int64           `json:"exit_code"`
	Output   json.RawMessage `json:"output,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type Options struct {
	RepoPath string
}

type Result struct {
	ExitCode     int
	Summary      json.RawMessage
	Workers      []WorkerResult
	UsageSummary *UsageSummary
}

type WorkerResult struct {
	MissionID     string
	Contract      string
	Vulnerability string
	ExitCode      int64
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

type aggregatedWorkerEntry struct {
	MissionID     string          `json:"mission_id"`
	Contract      string          `json:"contract"`
	Vulnerability string          `json:"vulnerability"`
	ExitCode      int64           `json:"exit_code"`
	Success       bool            `json:"success"`
	Error         string          `json:"error,omitempty"`
	Results       json.RawMessage `json:"results,omitempty"`
}

type runSummary struct {
	RunID       string                  `json:"run_id"`
	GeneratedAt string                  `json:"generated_at"`
	ExitCode    int                     `json:"exit_code"`
	Error       string                  `json:"error,omitempty"`
	Coordinator json.RawMessage         `json:"coordinator,omitempty"`
	Workers     []aggregatedWorkerEntry `json:"workers"`
	Usage       *UsageSummary           `json:"usage_summary,omitempty"`
}

func buildRunSummary(runID string, exitCode int, coordinatorRaw json.RawMessage, workers []WorkerResult, runErr error) (json.RawMessage, *UsageSummary, error) {
	summary := runSummary{
		RunID:       runID,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		ExitCode:    exitCode,
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
			ExitCode:      w.ExitCode,
			Success:       w.Err == nil && w.ExitCode == 0,
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
	cli              *rabbitclient.Client
	activeWorkers    atomic.Int32
	totalWorkers     atomic.Int32
	completedWorkers atomic.Int32
	currentPhase     atomic.Int32
}

func (e *Engine) ActiveWorkers() int32 {
	return e.activeWorkers.Load()
}

func (e *Engine) QueuedWorkers() int32 {
	queued := e.totalWorkers.Load() - e.activeWorkers.Load() - e.completedWorkers.Load()
	if queued < 0 {
		return 0
	}
	return queued
}

const (
	PHASE_INITIALIZING         = 0
	PHASE_READY                = 1
	PHASE_PREPARING_REPO_SPECS = 2
	PHASE_RUNNING_COORDINATOR  = 3
	PHASE_LOADING_MISSIONS     = 4
	PHASE_WORKING              = 5
)

func (e *Engine) Phase() int32 {
	return e.currentPhase.Load()
}

func (e *Engine) setPhase(status int32) {
	e.currentPhase.Store(status)
}

func NewEngine(amqpURL, queueName string) (*Engine, error) {
	cli, err := rabbitclient.Dial(amqpURL, queueName)
	if err != nil {
		return nil, err
	}
	e := &Engine{cli: cli}
	e.setPhase(PHASE_READY)
	return e, nil
}

func (e *Engine) Close() error {
	return e.cli.Close()
}

func (e *Engine) Execute(ctx context.Context, opts Options) (result *Result, resultErr error) {
	absPath, err := filepath.Abs(opts.RepoPath)
	if err != nil {
		return nil, fmt.Errorf("invalid exploration target path: %w", err)
	}

	e.setPhase(PHASE_PREPARING_REPO_SPECS)
	slug := e.getRepoSpecs(absPath)

	randBytes := make([]byte, 4)
	_, _ = rand.Read(randBytes)
	randomPart := hex.EncodeToString(randBytes)
	runID := fmt.Sprintf("%s-%d-%s", slug, time.Now().UnixNano(), randomPart)

	var (
		coordinatorRaw json.RawMessage
		workers        []WorkerResult
	)

	defer func() {
		exitCode := 1
		if result != nil {
			exitCode = result.ExitCode
		}

		summaryData, usage, sumErr := buildRunSummary(runID, exitCode, coordinatorRaw, workers, resultErr)
		if result == nil {
			result = &Result{ExitCode: exitCode}
		}
		if sumErr != nil {
			if resultErr == nil {
				resultErr = fmt.Errorf("building run summary: %w", sumErr)
			}
			return
		}
		result.Summary = summaryData
		result.UsageSummary = usage
	}()

	e.setPhase(PHASE_RUNNING_COORDINATOR)

	coordRaw, err := e.cli.Call(ctx, "coordinator", coordinatorTask{
		RunID:     runID,
		SkipBuild: false,
	})
	if err != nil {
		return nil, fmt.Errorf("submitting coordinator task: %w", err)
	}

	var coordResult containerResult
	if err := json.Unmarshal(coordRaw, &coordResult); err != nil {
		return nil, fmt.Errorf("parsing coordinator task response: %w", err)
	}
	coordinatorRaw = coordResult.Output

	if coordResult.ExitCode != 0 {
		result = &Result{ExitCode: int(coordResult.ExitCode)}
		return result, nil
	}
	if coordResult.Error != "" {
		return nil, fmt.Errorf("coordinator task reported error: %s", coordResult.Error)
	}

	e.setPhase(PHASE_LOADING_MISSIONS)
	missions, err := parseMissions(coordResult.Output)
	if err != nil {
		return nil, err
	}
	missionIndex, err := indexMissionsByID(coordResult.Output)
	if err != nil {
		return nil, err
	}

	e.setPhase(PHASE_WORKING)
	workers = e.runWorkers(ctx, runID, missionIndex, missions)

	overallExit := 0
	for _, w := range workers {
		if w.Err != nil || w.ExitCode != 0 {
			overallExit = 1
			break
		}
	}

	result = &Result{
		ExitCode: overallExit,
		Workers:  workers,
	}
	return result, nil
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

func (e *Engine) runWorkers(ctx context.Context, runID string, missionIndex map[string]json.RawMessage, missions []missionSummary) []WorkerResult {
	if len(missions) == 0 {
		return nil
	}

	e.totalWorkers.Store(int32(len(missions)))
	e.completedWorkers.Store(0)

	results := make([]WorkerResult, len(missions))
	var wg sync.WaitGroup

	for i, m := range missions {
		wg.Add(1)
		go func(i int, m missionSummary) {
			e.activeWorkers.Add(1)
			defer e.activeWorkers.Add(-1)
			defer e.completedWorkers.Add(1)
			defer wg.Done()

			missionRaw, ok := missionIndex[m.ID]
			if !ok {
				results[i] = WorkerResult{
					MissionID: m.ID, Contract: m.Contract, Vulnerability: m.Vulnerability,
					Err: fmt.Errorf("mission %q not found in coordinator output", m.ID),
				}
				return
			}

			raw, err := e.cli.Call(ctx, "worker", workerTask{
				RunID:     runID,
				MissionID: m.ID,
				Mission:   missionRaw,
			})
			if err != nil {
				results[i] = WorkerResult{
					MissionID: m.ID, Contract: m.Contract, Vulnerability: m.Vulnerability,
					Err: fmt.Errorf("submitting worker task: %w", err),
				}
				return
			}

			var wr containerResult
			if err := json.Unmarshal(raw, &wr); err != nil {
				results[i] = WorkerResult{
					MissionID: m.ID, Contract: m.Contract, Vulnerability: m.Vulnerability,
					Err: fmt.Errorf("parsing worker task response: %w", err),
				}
				return
			}

			results[i] = WorkerResult{
				MissionID:     m.ID,
				Contract:      m.Contract,
				Vulnerability: m.Vulnerability,
				ExitCode:      wr.ExitCode,
				ResultsRaw:    wr.Output,
				ReadErrMsg:    wr.Error,
			}
		}(i, m)
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
