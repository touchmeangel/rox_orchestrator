package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/touchmeangel/ignite_orchestrator/config"
	"github.com/touchmeangel/ignite_orchestrator/dockerx"
)

const CoordinatorImage = "touchmeangel/ignite_coordinator:latest"
const WorkerImage = "touchmeangel/ignite_worker:latest"

type Options struct {
	GithubURL         string
	InspectPath       string
	ForceFresh        bool
	SkipBuild         bool
	Config            *config.Config
	WorkerConcurrency int
}

type Result struct {
	WorkspacePath string
	ResultsFile   string
	ExitCode      int
	Workers       []WorkerResult
}

type WorkerResult struct {
	MissionID     string
	Contract      string
	Vulnerability string
	ExitCode      int64
	ResultsFile   string
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

func loadMissions(resultsPath string) ([]missionSummary, error) {
	data, err := os.ReadFile(resultsPath)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", resultsPath, err)
	}
	var out coordinatorOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", resultsPath, err)
	}
	return out.Missions, nil
}

type Engine struct {
	dockerCli     *dockerx.Client
	runner        Runner
	activeWorkers atomic.Int32
	currentPhase  atomic.Int32
}

func (e *Engine) ActiveWorkers() int32 {
	return e.activeWorkers.Load()
}

const PHASE_INITIALIZING = 0
const PHASE_READY = 1
const PHASE_PREPARING_REPO_SPECS = 2
const PHASE_RUNNING_COORDINATOR = 3
const PHASE_LOADING_MISSIONS = 4
const PHASE_WORKING = 5

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

func (e *Engine) VerifyDaemonIsRunning(ctx context.Context) bool {
	return e.dockerCli.Ping(ctx)
}

func (e *Engine) SyncImage(ctx context.Context) error {
	err := e.dockerCli.PullImage(ctx, CoordinatorImage)
	if err != nil {
		return err
	}
	err = e.dockerCli.PullImage(ctx, WorkerImage)
	if err != nil {
		return err
	}
	return nil
}

func (e *Engine) Execute(ctx context.Context, opts Options) (*Result, error) {
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
	baseWorkPath := filepath.Join(config.IgniteHome, "workspaces", runID)

	defer func() {
		_ = os.RemoveAll(baseWorkPath)
	}()

	coordWorkPath := filepath.Join(baseWorkPath, "coordinator")
	if err := os.MkdirAll(coordWorkPath, 0o755); err != nil {
		return nil, fmt.Errorf("failed creating coordinator runtime directory: %w", err)
	}

	debugPath := filepath.Join(config.IgniteHome, "debug.log")
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
		Image: CoordinatorImage,
		Name:  fmt.Sprintf("ignite-coordinator-%s", runID),
		Cmd:   cmdArgs,
		Env:   env,
		Mounts: []dockerx.Mount{
			{Source: repoPath, Target: "/repo", ReadOnly: true},
			{Source: coordWorkPath, Target: "/work", ReadOnly: false},
			{Source: configPath, Target: "/app/config.json", ReadOnly: true},
			{Source: debugPath, Target: "/app/debug.log", ReadOnly: false},
		},
	}

	e.setPhase(PHASE_RUNNING_COORDINATOR)
	code, err := e.runner.Run(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("core machine execution failure state: %w", err)
	}
	if code != 0 {
		return &Result{WorkspacePath: coordWorkPath, ResultsFile: resultsPath, ExitCode: int(code)}, nil
	}

	e.setPhase(PHASE_LOADING_MISSIONS)
	missions, err := loadMissions(resultsPath)
	if err != nil {
		return nil, fmt.Errorf("reading coordinator missions: %w", err)
	}

	e.setPhase(PHASE_WORKING)
	workers := e.runWorkers(ctx, workerRunConfig{
		repoPath:     repoPath,
		baseWorkPath: baseWorkPath,
		runID:        runID,
		configPath:   configPath,
		missionsFile: resultsPath,
		missions:     missions,
		env:          env,
		concurrency:  opts.WorkerConcurrency,
	})

	overallExit := 0
	for _, w := range workers {
		if w.Err != nil || w.ExitCode != 0 {
			overallExit = 1
			break
		}
	}

	return &Result{
		WorkspacePath: coordWorkPath,
		ResultsFile:   resultsPath,
		ExitCode:      overallExit,
		Workers:       workers,
	}, nil
}

type workerRunConfig struct {
	repoPath     string
	baseWorkPath string
	runID        string
	configPath   string
	missionsFile string
	missions     []missionSummary
	env          []string
	concurrency  int
}

func (e *Engine) runWorkers(ctx context.Context, cfg workerRunConfig) []WorkerResult {
	if len(cfg.missions) == 0 {
		return nil
	}

	concurrency := cfg.concurrency
	if concurrency > len(cfg.missions) {
		concurrency = len(cfg.missions)
	}

	results := make([]WorkerResult, len(cfg.missions))
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup

	workerEnv := append(cfg.env, "IGNITE_HEADLESS=1")

	for i, m := range cfg.missions {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, m missionSummary) {
			e.activeWorkers.Add(1)
			defer e.activeWorkers.Add(-1)

			defer wg.Done()
			defer func() { <-sem }()

			missionWorkPath := filepath.Join(cfg.baseWorkPath, "missions", m.ID)
			if err := os.MkdirAll(missionWorkPath, 0o755); err != nil {
				results[i] = WorkerResult{MissionID: m.ID, Err: fmt.Errorf("preparing mission folder: %w", err)}
				return
			}

			resultsFile := filepath.Join(missionWorkPath, fmt.Sprintf("worker_%s.json", m.ID))
			debugPath := filepath.Join(missionWorkPath, "agent.log")
			if err := touchFile(debugPath); err != nil {
				results[i] = WorkerResult{MissionID: m.ID, Err: fmt.Errorf("preparing worker debug log: %w", err)}
				return
			}

			spec := dockerx.RunSpec{
				Image: WorkerImage,
				Name:  fmt.Sprintf("ignite-worker-%s-%s", cfg.runID, m.ID),
				Cmd: []string{
					"--repo-path", "/repo",
					"--work-path", "/work",
					"--output", "/work/" + filepath.Base(resultsFile),
					"--debug", "/app/debug.log",
					"--missions-file", "/app/coordinator_results.json",
					"--mission-id", m.ID,
				},
				Env: workerEnv,
				Mounts: []dockerx.Mount{
					{Source: cfg.repoPath, Target: "/repo", ReadOnly: true},
					{Source: missionWorkPath, Target: "/work", ReadOnly: false},
					{Source: cfg.configPath, Target: "/app/config.json", ReadOnly: true},
					{Source: debugPath, Target: "/app/debug.log", ReadOnly: false},
					{Source: cfg.missionsFile, Target: "/app/coordinator_results.json", ReadOnly: true},
				},
				LogPrefix: fmt.Sprintf("[%s] ", m.ID),
				Quiet:     true,
			}

			code, err := e.runner.Run(ctx, spec)

			results[i] = WorkerResult{
				MissionID:     m.ID,
				Contract:      m.Contract,
				Vulnerability: m.Vulnerability,
				ExitCode:      code,
				ResultsFile:   resultsFile,
				Err:           err,
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
