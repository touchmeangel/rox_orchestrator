package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

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
	dockerCli *dockerx.Client
}

func NewEngine() (*Engine, error) {
	cli, err := dockerx.New()
	if err != nil {
		return nil, fmt.Errorf("initializing docker core client: %w", err)
	}
	return &Engine{dockerCli: cli}, nil
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

	repoPath, slug, err := e.prepareRepoSpecs(opts.GithubURL, absPath, opts.ForceFresh)
	if err != nil {
		return nil, err
	}

	workPath := filepath.Join(config.IgniteHome, "workspaces", slug)
	if err := os.MkdirAll(workPath, 0o755); err != nil {
		return nil, fmt.Errorf("failed creating unique runtime environment workspace: %w", err)
	}

	debugPath := filepath.Join(config.IgniteHome, "debug.log")
	configPath := config.ConfigPath()
	resultsPath := filepath.Join(workPath, "coordinator_results.json")

	envMap := config.LoadEnvVars(opts.Config)
	var env []string
	for k, v := range envMap {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	cmdArgs := []string{"--repo-path", "/repo", "--work-path", "/work", "--output", "/work/coordinator_results.json", "--debug", "/app/debug.log"}
	if opts.SkipBuild {
		cmdArgs = append(cmdArgs, "--skip-build")
	}

	spec := dockerx.RunSpec{
		Image: CoordinatorImage,
		Name:  "ignite-coordinator",
		Cmd:   cmdArgs,
		Env:   env,
		Mounts: []dockerx.Mount{
			{Source: repoPath, Target: "/repo", ReadOnly: true},
			{Source: workPath, Target: "/work", ReadOnly: false},
			{Source: configPath, Target: "/app/config.json", ReadOnly: true},
			{Source: debugPath, Target: "/app/debug.log", ReadOnly: false},
		},
	}

	code, err := e.dockerCli.Run(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("core machine execution failure state: %w", err)
	}
	if code != 0 {
		return &Result{WorkspacePath: workPath, ResultsFile: resultsPath, ExitCode: int(code)}, nil
	}

	missions, err := loadMissions(resultsPath)
	if err != nil {
		return nil, fmt.Errorf("reading coordinator missions: %w", err)
	}

	workers := e.runWorkers(ctx, workerRunConfig{
		repoPath:     repoPath,
		workPath:     workPath,
		slug:         slug,
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
		WorkspacePath: workPath,
		ResultsFile:   resultsPath,
		ExitCode:      overallExit,
		Workers:       workers,
	}, nil
}

type workerRunConfig struct {
	repoPath     string
	workPath     string
	slug         string
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

	for i, m := range cfg.missions {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, m missionSummary) {
			defer wg.Done()
			defer func() { <-sem }()

			resultsFile := filepath.Join(cfg.workPath, fmt.Sprintf("worker_%s.json", m.ID))

			debugPath := filepath.Join(cfg.workPath, fmt.Sprintf("worker_%s_debug.log", m.ID))
			if err := touchFile(debugPath); err != nil {
				results[i] = WorkerResult{MissionID: m.ID, Err: fmt.Errorf("preparing worker debug log: %w", err)}
				return
			}

			spec := dockerx.RunSpec{
				Image: WorkerImage,
				Name:  fmt.Sprintf("ignite-worker-%s-%s", cfg.slug, m.ID),
				Cmd: []string{
					"--repo-path", "/repo",
					"--work-path", "/work",
					"--output", "/work/" + filepath.Base(resultsFile),
					"--debug", "/app/debug.log",
					"--missions-file", "/app/coordinator_results.json",
					"--mission-id", m.ID,
				},
				Env: cfg.env,
				Mounts: []dockerx.Mount{
					{Source: cfg.repoPath, Target: "/repo", ReadOnly: true},
					{Source: cfg.workPath, Target: "/work", ReadOnly: false},
					{Source: cfg.configPath, Target: "/app/config.json", ReadOnly: true},
					{Source: debugPath, Target: "/app/debug.log", ReadOnly: false},
					{Source: cfg.missionsFile, Target: "/app/coordinator_results.json", ReadOnly: true},
				},
				LogPrefix: fmt.Sprintf("[%s] ", m.ID),
			}

			code, err := e.dockerCli.Run(ctx, spec)
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
