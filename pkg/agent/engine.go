package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/touchmeangel/ignite_orchestrator/config"
	"github.com/touchmeangel/ignite_orchestrator/dockerx"
)

const CoordinatorImage = "touchmeangel/ignite_coordinator:latest"
const WorkerImage = "touchmeangel/ignite_worker:latest"

type Options struct {
	GithubURL   string
	InspectPath string
	ForceFresh  bool
	SkipBuild   bool
	Config      *config.Config
}

type Result struct {
	WorkspacePath string
	ResultsFile   string
	ExitCode      int
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
	return e.dockerCli.PullImage(ctx, CoordinatorImage)
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

	return &Result{
		WorkspacePath: workPath,
		ResultsFile:   resultsPath,
		ExitCode:      int(code),
	}, nil
}
