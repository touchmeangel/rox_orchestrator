package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/touchmeangel/rox_orchestrator/config"
	"github.com/touchmeangel/rox_orchestrator/dockerx"
	"github.com/touchmeangel/rox_orchestrator/ui"
)

const (
	Image = "touchmeangel/rox_agent:latest"
)

type Options struct {
	GithubURL         string
	InspectPath       string
	ForceFresh        bool
	SkipBuild         bool
	Config            *config.Config
	WorkerConcurrency int
}

type Result struct {
	ExitCode     int
	ResultsFile  string
	ResultsDir   string
	Workers      []WorkerResult
	UsageSummary *UsageSummary
}

type WorkerResult struct {
	MissionID     string
	Contract      string
	Vulnerability string
	ExitCode      int64
	ResultsFile   string
	ResultsRaw    json.RawMessage
	LogFile       string
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

type runPaths struct {
	dir            string
	workersLogDir  string
	coordinatorLog string
	resultFile     string
}

func newRunPaths(runID string) runPaths {
	dir := filepath.Join(config.RoxHome, "results", runID)
	return runPaths{
		dir:            dir,
		workersLogDir:  filepath.Join(dir, "workers"),
		coordinatorLog: filepath.Join(dir, "coordinator.log"),
		resultFile:     filepath.Join(dir, "result.json"),
	}
}

type aggregatedWorkerEntry struct {
	MissionID     string          `json:"mission_id"`
	Contract      string          `json:"contract"`
	Vulnerability string          `json:"vulnerability"`
	ExitCode      int64           `json:"exit_code"`
	Success       bool            `json:"success"`
	LogFile       string          `json:"log_file,omitempty"`
	Error         string          `json:"error,omitempty"`
	Results       json.RawMessage `json:"results,omitempty"`
}

type runSummary struct {
	RunID          string                  `json:"run_id"`
	GeneratedAt    string                  `json:"generated_at"`
	ExitCode       int                     `json:"exit_code"`
	Error          string                  `json:"error,omitempty"`
	CoordinatorLog string                  `json:"coordinator_log,omitempty"`
	Coordinator    json.RawMessage         `json:"coordinator,omitempty"`
	Workers        []aggregatedWorkerEntry `json:"workers"`
	Usage          *UsageSummary           `json:"usage_summary,omitempty"`
}

func writeRunSummary(paths runPaths, runID string, exitCode int, coordinatorRaw json.RawMessage, workers []WorkerResult, runErr error) (string, *UsageSummary, error) {
	summary := runSummary{
		RunID:          runID,
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		ExitCode:       exitCode,
		CoordinatorLog: paths.coordinatorLog,
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
			LogFile:       w.LogFile,
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

	if err := os.MkdirAll(paths.dir, 0o755); err != nil {
		return "", nil, fmt.Errorf("creating results directory: %w", err)
	}

	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return "", nil, fmt.Errorf("marshaling run summary: %w", err)
	}
	if err := os.WriteFile(paths.resultFile, data, 0o644); err != nil {
		return "", nil, fmt.Errorf("writing run summary: %w", err)
	}

	return paths.resultFile, usage, nil
}

type Engine struct {
	dockerCli        *dockerx.Client
	runner           Runner
	activeWorkers    atomic.Int32
	totalWorkers     atomic.Int32
	completedWorkers atomic.Int32
	currentPhase     atomic.Int32
	live             dockerx.LineWriter
}

func (e *Engine) SetLive(l dockerx.LineWriter) {
	e.live = l
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

func NewEngine() (*Engine, error) {
	cli, err := dockerx.New()
	if err != nil {
		return nil, fmt.Errorf("initializing docker core client: %w", err)
	}
	e := &Engine{
		dockerCli: cli,
		runner:    cli,
	}
	e.setPhase(PHASE_READY)
	return e, nil
}

func (e *Engine) Close() error {
	return e.dockerCli.Close()
}

func (e *Engine) VerifyDaemonIsRunning(ctx context.Context) error {
	return e.dockerCli.Ping(ctx)
}

func (e *Engine) SyncImage(ctx context.Context) error {
	refs := []string{Image}

	bar := ui.NewMultiPullProgress("Pulling RoX images", refs)
	bar.Start()
	defer bar.Stop()

	for i, ref := range refs {
		if err := e.pullOne(ctx, bar, i, ref); err != nil {
			bar.Fail(i, err)
			return fmt.Errorf("pulling %s: %w", ref, err)
		}
		bar.Advance(i)
	}
	return nil
}

type layerProgress struct {
	current, total int64
	done           bool
}

func (e *Engine) pullOne(ctx context.Context, bar *ui.MultiPullProgress, index int, ref string) error {
	layers := map[string]*layerProgress{}
	var mu sync.Mutex

	onProgress := func(status, id string, current, total int64) {
		mu.Lock()
		defer mu.Unlock()

		if id != "" {
			lp, ok := layers[id]
			if !ok {
				lp = &layerProgress{}
				layers[id] = lp
			}
			switch {
			case status == "Pull complete" || status == "Already exists":
				lp.done = true
				if lp.total > 0 {
					lp.current = lp.total
				}
			case total > 0:
				lp.current, lp.total = current, total
			}
		}

		var curSum, totalSum int64
		for _, lp := range layers {
			curSum += lp.current
			totalSum += lp.total
		}

		phase := ui.ParsePhase(status)
		if totalSum > 0 {
			phase = ui.PhaseDownloading
		}

		bar.Update(index, phase, curSum, totalSum)
	}

	return e.dockerCli.PullImage(ctx, ref, onProgress)
}

func syncWorkspaceFiles(repositoryPath string, workspacePath string) error {
	return filepath.Walk(repositoryPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(repositoryPath, path)
		if err != nil {
			return err
		}

		dst := filepath.Join(workspacePath, rel)

		if info.IsDir() {
			return os.MkdirAll(dst, info.Mode())
		}

		return copyFile(path, dst, info.Mode())
	})
}

func copyFile(src, dst string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	if err := out.Sync(); err != nil {
		return err
	}

	return os.Chmod(dst, perm|0o200)
}

func (e *Engine) Execute(ctx context.Context, opts Options) (result *Result, resultErr error) {
	inspectPath := opts.InspectPath
	if inspectPath == "" {
		inspectPath = "."
	}
	absPath, err := filepath.Abs(inspectPath)
	if err != nil {
		return nil, fmt.Errorf("invalid exploration target path: %w", err)
	}

	e.setPhase(PHASE_PREPARING_REPO_SPECS)
	repoPath, slug, err := e.prepareRepoSpecs(opts.GithubURL, absPath, opts.ForceFresh)
	if err != nil {
		return nil, err
	}

	randBytes := make([]byte, 4)
	_, _ = rand.Read(randBytes)
	randomPart := hex.EncodeToString(randBytes)

	runID := fmt.Sprintf("%s-%d-%s", slug, time.Now().UnixNano(), randomPart)
	baseWorkPath := filepath.Join(config.RoxHome, "workspaces", runID)
	paths := newRunPaths(runID)

	if err := os.MkdirAll(paths.workersLogDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating results directory: %w", err)
	}
	if err := touchFile(paths.coordinatorLog); err != nil {
		return nil, fmt.Errorf("preparing coordinator debug log: %w", err)
	}

	var (
		coordinatorRaw json.RawMessage
		workers        []WorkerResult
	)

	defer func() {
		exitCode := 1
		if result != nil {
			exitCode = result.ExitCode
		}

		summaryPath, usage, sumErr := writeRunSummary(paths, runID, exitCode, coordinatorRaw, workers, resultErr)

		if result == nil {
			result = &Result{ExitCode: exitCode}
		}
		if sumErr != nil {
			fmt.Fprintf(os.Stderr, "  ✗ failed to write run summary: %v\n", sumErr)
		} else {
			result.ResultsFile = summaryPath
			result.ResultsDir = paths.dir
			result.UsageSummary = usage
		}

		if rmErr := os.RemoveAll(baseWorkPath); rmErr != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ failed to clean up workspace %s: %v\n", baseWorkPath, rmErr)
		}
	}()

	coordWorkPath := filepath.Join(baseWorkPath, "coordinator")
	if err := os.MkdirAll(coordWorkPath, 0o755); err != nil {
		return nil, fmt.Errorf("failed creating coordinator runtime directory: %w", err)
	}
	if err := syncWorkspaceFiles(repoPath, coordWorkPath); err != nil {
		return nil, fmt.Errorf("failed syncing workspace files: %w", err)
	}

	configPath := config.ConfigPath()
	resultsPath := filepath.Join(coordWorkPath, "coordinator_results.json")

	envMap := config.LoadEnvVars(opts.Config)
	var env []string
	for k, v := range envMap {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	cmdArgs := []string{
		"--repo-path", "/repo",
		"--work-path", "/work",
		"--output", "/work/coordinator_results.json",
		"--debug", "/app/debug.log",
	}
	if opts.SkipBuild {
		cmdArgs = append(cmdArgs, "--skip-build")
	}

	spec := dockerx.RunSpec{
		Image: Image,
		Name:  fmt.Sprintf("rox-coordinator-%s", runID),
		Cmd:   cmdArgs,
		Env:   env,
		Live:  e.live,
		Mounts: []dockerx.Mount{
			{Source: repoPath, Target: "/repo", ReadOnly: true},
			{Source: coordWorkPath, Target: "/work", ReadOnly: false},
			{Source: configPath, Target: "/app/config.json", ReadOnly: true},
			{Source: paths.coordinatorLog, Target: "/app/debug.log", ReadOnly: false}, // CHANGED
		},
	}

	e.setPhase(PHASE_RUNNING_COORDINATOR)

	if s, ok := e.live.(dockerx.Suspendable); ok {
		s.Suspend()
	}

	code, err := e.runner.Run(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("core machine execution failure state: %w", err)
	}
	if code != 0 {
		if rmErr := os.RemoveAll(coordWorkPath); rmErr != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ failed to clean up coordinator workspace %s: %v\n", coordWorkPath, rmErr)
		}
		result = &Result{ExitCode: int(code)}
		return result, nil
	}

	if s, ok := e.live.(dockerx.Suspendable); ok {
		s.Resume()
	}

	e.setPhase(PHASE_LOADING_MISSIONS)
	raw, err := os.ReadFile(resultsPath)
	if err != nil {
		_ = os.RemoveAll(coordWorkPath)
		return nil, fmt.Errorf("reading coordinator missions: %w", err)
	}
	coordinatorRaw = json.RawMessage(raw)

	missions, err := parseMissions(raw)
	if err != nil {
		_ = os.RemoveAll(coordWorkPath)
		return nil, fmt.Errorf("reading coordinator missions: %w", err)
	}

	if rmErr := os.RemoveAll(coordWorkPath); rmErr != nil {
		fmt.Fprintf(os.Stderr, "  ⚠ failed to clean up coordinator workspace %s: %v\n", coordWorkPath, rmErr)
	}

	e.setPhase(PHASE_WORKING)
	workers = e.runWorkers(ctx, workerRunConfig{
		repoPath:      repoPath,
		baseWorkPath:  baseWorkPath,
		runID:         runID,
		configPath:    configPath,
		missionsFile:  resultsPath,
		missions:      missions,
		env:           env,
		concurrency:   opts.WorkerConcurrency,
		workersLogDir: paths.workersLogDir,
	})

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

type workerRunConfig struct {
	repoPath      string
	baseWorkPath  string
	runID         string
	configPath    string
	missionsFile  string
	missions      []missionSummary
	env           []string
	concurrency   int
	workersLogDir string
}

func (e *Engine) runWorkers(ctx context.Context, cfg workerRunConfig) []WorkerResult {
	if len(cfg.missions) == 0 {
		return nil
	}

	e.totalWorkers.Store(int32(len(cfg.missions)))
	e.completedWorkers.Store(0)

	concurrency := cfg.concurrency
	if concurrency > len(cfg.missions) {
		concurrency = len(cfg.missions)
	}

	results := make([]WorkerResult, len(cfg.missions))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	for i, m := range cfg.missions {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, m missionSummary) {
			e.activeWorkers.Add(1)
			defer e.activeWorkers.Add(-1)
			defer e.completedWorkers.Add(1)
			defer wg.Done()
			defer func() { <-sem }()

			missionWorkPath := filepath.Join(cfg.baseWorkPath, "missions", m.ID)
			if err := os.MkdirAll(missionWorkPath, 0o755); err != nil {
				results[i] = WorkerResult{MissionID: m.ID, Err: fmt.Errorf("preparing mission folder: %w", err)}
				return
			}
			if err := syncWorkspaceFiles(cfg.repoPath, missionWorkPath); err != nil {
				results[i] = WorkerResult{MissionID: m.ID, Err: fmt.Errorf("failed syncing workspace files: %w", err)}
				_ = os.RemoveAll(missionWorkPath)
				return
			}

			resultsFile := filepath.Join(missionWorkPath, fmt.Sprintf("worker_%s.json", m.ID))
			workerLogFile := filepath.Join(cfg.workersLogDir, fmt.Sprintf("worker_%s.log", m.ID)) // CHANGED
			if err := touchFile(workerLogFile); err != nil {                                      // CHANGED
				results[i] = WorkerResult{MissionID: m.ID, Err: fmt.Errorf("preparing worker debug log: %w", err)}
				_ = os.RemoveAll(missionWorkPath)
				return
			}

			spec := dockerx.RunSpec{
				Image: Image,
				Name:  fmt.Sprintf("rox-worker-%s-%s", cfg.runID, m.ID),
				Cmd: []string{
					"--repo-path", "/repo",
					"--work-path", "/work",
					"--output", "/work/" + filepath.Base(resultsFile),
					"--debug", "/app/debug.log",
					"--missions-file", "/app/coordinator_results.json",
					"--mission-id", m.ID,
				},
				Env:  cfg.env,
				Live: e.live,
				Mounts: []dockerx.Mount{
					{Source: cfg.repoPath, Target: "/repo", ReadOnly: true},
					{Source: missionWorkPath, Target: "/work", ReadOnly: false},
					{Source: cfg.configPath, Target: "/app/config.json", ReadOnly: true},
					{Source: workerLogFile, Target: "/app/debug.log", ReadOnly: false}, // CHANGED
					{Source: cfg.missionsFile, Target: "/app/coordinator_results.json", ReadOnly: true},
				},
				LogPrefix: fmt.Sprintf("[%s] ", m.ID),
				Quiet:     true,
			}

			code, runErr := e.runner.Run(ctx, spec)

			var raw json.RawMessage
			var readErrMsg string
			if data, rerr := os.ReadFile(resultsFile); rerr == nil {
				raw = json.RawMessage(data)
			} else if runErr == nil && code == 0 {
				readErrMsg = fmt.Sprintf("could not read worker results file: %v", rerr)
			}

			if rmErr := os.RemoveAll(missionWorkPath); rmErr != nil {
				fmt.Fprintf(os.Stderr, "  ⚠ failed to clean up mission workspace %s: %v\n", missionWorkPath, rmErr)
			}

			results[i] = WorkerResult{
				MissionID:     m.ID,
				Contract:      m.Contract,
				Vulnerability: m.Vulnerability,
				ExitCode:      code,
				ResultsFile:   resultsFile,
				ResultsRaw:    raw,
				LogFile:       workerLogFile,
				ReadErrMsg:    readErrMsg,
				Err:           runErr,
			}
		}(i, m)
	}

	wg.Wait()
	return results
}

func touchFile(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
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
