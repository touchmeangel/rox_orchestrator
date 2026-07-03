package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/touchmeangel/ignite_orchestrator/config"
	"github.com/touchmeangel/ignite_orchestrator/pkg/agent"
	"github.com/touchmeangel/ignite_orchestrator/ui"
)

const WorkerConcurrency = 4

var spinnerFrames = []string{"·", "*", "✷", "✸", "✹", "✺", "✹", "✸", "✷", "*"}

func main() {
	args := os.Args[1:]
	var githubURL string
	var flags []string
	var positional []string

	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--github-url" || a == "-g" {
			if i+1 < len(args) {
				githubURL = args[i+1]
				i++
				continue
			}
			fmt.Printf("  %s  Missing value for flag token allocation option %s.\n", ui.Red("✗"), a)
			os.Exit(1)
		}
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
		} else {
			positional = append(positional, a)
		}
	}

	if hasFlag(flags, "--help", "-h") {
		usage()
		os.Exit(0)
	}

	reconfigure := hasFlag(flags, "--reconfigure", "-r")
	doUpdate := hasFlag(flags, "--update", "-u")
	forceReclone := hasFlag(flags, "--fresh")
	skipBuild := hasFlag(flags, "--skip-build")

	if len(positional) > 1 {
		usage()
		os.Exit(1)
	}

	inspectPath := "."
	if len(positional) > 0 {
		inspectPath = positional[0]
	}

	debugPath, err := config.EnsureEnvironment()
	if err != nil {
		fmt.Fprintf(os.Stderr, "  ✗ environment initialization failure state: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("  %s %s\n", ui.Dim("➔ Debug:"), ui.Cyan(debugPath))

	coreEngine, err := agent.NewEngine()
	if err != nil {
		fmt.Printf("  %s Failed initializing runtime client subsystem: %v\n", ui.Red("✗"), err)
		os.Exit(1)
	}
	defer coreEngine.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if !coreEngine.VerifyDaemonIsRunning(ctx) {
		fmt.Println("  " + ui.Red("✗") + "  Docker is not running. Start Docker Desktop and retry.")
		os.Exit(1)
	}

	var cfg *config.Config
	if config.Exists() && !reconfigure {
		cfg, err = config.Load()
		if err == nil {
			renderConfigMetadata(cfg)
		}
	} else {
		if reconfigure {
			fmt.Printf("  %s  Docker available\n", ui.Cyan("✔"))
			cfg, err = config.RunReconfigure()
			if err != nil {
				if err == ui.ErrAborted {
					fmt.Println("\n  " + ui.Dim("Configuration cancelled. Exiting cleanly."))
					os.Exit(0)
				}
				fmt.Fprintf(os.Stderr, "\n  %s %v\n", ui.Red("✗"), err)
				os.Exit(1)
			}
		} else {
			fmt.Println("  " + ui.Dim("No config found — running first-time setup."))
			fmt.Printf("  %s  Docker available\n", ui.Cyan("✔"))
			cfg, err = config.RunSetup()
			if err != nil {
				if err == ui.ErrAborted {
					fmt.Println("\n  " + ui.Dim("Configuration cancelled. Exiting cleanly."))
					os.Exit(0)
				}
				fmt.Fprintf(os.Stderr, "\n  %s %v\n", ui.Red("✗"), err)
				os.Exit(1)
			}
		}
	}

	if doUpdate {
		fmt.Printf("  %s\n", ui.Dim("Pulling latest coordinator and worker images..."))
		if err := coreEngine.SyncImage(ctx); err != nil {
			fmt.Printf("  %s Synchronization image update tracking error: %v\n", ui.Red("✗"), err)
			os.Exit(1)
		}
		fmt.Printf("  %s  Images updated.\n", ui.Cyan("✔"))
	}

	opts := agent.Options{
		GithubURL:         githubURL,
		InspectPath:       inspectPath,
		ForceFresh:        forceReclone,
		SkipBuild:         skipBuild,
		Config:            cfg,
		WorkerConcurrency: WorkerConcurrency,
	}

	if skipBuild {
		fmt.Printf("  %s  %s\n", ui.Yellow("⚠"), ui.Dim("--skip-build: build phase will be skipped"))
	}

	ui.Rule("Analysis Pipeline Execution")
	fmt.Println("  " + ui.Dim("Running core engine analysis processing sequence..."))
	fmt.Println()

	res, err := runWithLiveStatus(ctx, coreEngine, opts)

	if errors.Is(err, agent.ErrNoRepositoryDetected) {
		res, err = handleInteractivePathResolution(ctx, coreEngine, inspectPath, opts)
	}

	if err != nil {
		fmt.Printf("\n  %s %s\n", ui.Red("✗"), ui.Red(fmt.Sprintf("Pipeline engine aborted: %v", err)))
		os.Exit(1)
	}

	if res.ExitCode != 0 {
		fmt.Printf("\n  %s %s\n", ui.Red("✗"), ui.Red(ui.Bold("Container execution failed.")))
		printWorkerFailures(res.Workers)
		os.Exit(res.ExitCode)
	}

	completionText := ui.BoldCyan("AUDIT ENGINE PIPELINE COMPLETE\n\n") +
		ui.Dim("Status:     ") + ui.Bold("Active / Success\n") +
		ui.Dim("Artifacts:  ") + ui.Cyan(filepath.Base(res.ResultsFile)+"\n") +
		ui.Dim("Location:   ") + ui.Dim(res.WorkspacePath)

	ui.Panel(completionText)
	fmt.Println()
}

func phaseToString(phase int32) string {
	switch phase {
	case agent.PHASE_INITIALIZING:
		return "Initializing…"
	case agent.PHASE_READY:
		return "Ready…"
	case agent.PHASE_PREPARING_REPO_SPECS:
		return "Preparing repository specs…"
	case agent.PHASE_RUNNING_COORDINATOR:
		return "Running coordinator analysis…"
	case agent.PHASE_LOADING_MISSIONS:
		return "Loading target missions…"
	case agent.PHASE_WORKING:
		return "Starting workers…"
	default:
		return "Working…"
	}
}

func runWithLiveStatus(ctx context.Context, e *agent.Engine, opts agent.Options) (*agent.Result, error) {
	type outcome struct {
		res *agent.Result
		err error
	}
	done := make(chan outcome, 1)

	go func() {
		res, err := e.Execute(ctx, opts)
		done <- outcome{res, err}
	}()

	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()

	frame := 0
	printLiveStatus(spinnerFrames[frame], "Initializing…")

	for {
		select {
		case out := <-done:
			fmt.Print("\r\033[K")
			return out.res, out.err
		case <-ticker.C:
			frame = (frame + 1) % len(spinnerFrames)

			var label string
			if active := e.ActiveWorkers(); active > 0 {
				plural := "s"
				if active == 1 {
					plural = ""
				}
				label = fmt.Sprintf("%d worker%s active", active, plural)
			} else {
				label = phaseToString(e.Phase())
			}

			printLiveStatus(spinnerFrames[frame], label)
		}
	}
}

func printLiveStatus(frame, label string) {
	fmt.Printf("\r  %s %s\033[K", ui.Cyan(frame), ui.Dim(label))
}

func printWorkerFailures(workers []agent.WorkerResult) {
	if len(workers) == 0 {
		fmt.Printf("  %s\n", ui.Dim("No worker results available — the coordinator itself may have failed before any missions were created."))
		return
	}

	failed := 0
	for _, w := range workers {
		if w.Err == nil && w.ExitCode == 0 {
			continue
		}
		failed++

		label := w.MissionID
		if w.Contract != "" || w.Vulnerability != "" {
			label = fmt.Sprintf("%s  %s — %s", w.MissionID, w.Contract, w.Vulnerability)
		}
		fmt.Printf("\n  %s  %s\n", ui.Red("✗"), ui.Bold(label))

		if w.Err != nil {
			fmt.Printf("      %s %v\n", ui.Dim("engine error:"), w.Err)
		}
		if w.ExitCode != 0 {
			fmt.Printf("      %s %d\n", ui.Dim("container exit code:"), w.ExitCode)
		}
		if w.ResultsFile != "" {
			fmt.Printf("      %s %s\n", ui.Dim("results file:"), w.ResultsFile)
		}
	}

	if failed == 0 {
		fmt.Printf("  %s\n", ui.Dim("No individual worker failures recorded — check the coordinator's own output above."))
		return
	}

	plural := "s"
	if failed == 1 {
		plural = ""
	}
	fmt.Printf("\n  %d of %d worker%s failed — see per-mission detail above.\n", failed, len(workers), plural)
}

func handleInteractivePathResolution(ctx context.Context, e *agent.Engine, originalPath string, opts agent.Options) (*agent.Result, error) {
	fmt.Printf("  %s  No git repository found at %s\n", ui.Dim("⚠"), originalPath)

	choices := []string{"Use this directory as source", "Enter a GitHub URL to clone"}
	choice, err := ui.Select("How would you like to proceed?", choices, 0)
	if err != nil {
		handleAbort()
	}

	if choice == 1 {
		entered, err := ui.Text("GitHub URL", "")
		if err != nil || entered == "" {
			handleAbort()
		}
		opts.GithubURL = strings.TrimSpace(entered)
	} else {
		opts.GithubURL = ""
		opts.InspectPath = originalPath
	}

	fmt.Printf("  %s  Configuring context targets execution mapping structural boundaries...\n", ui.Cyan("✔"))
	return runWithLiveStatus(ctx, e, opts)
}

func renderConfigMetadata(cfg *config.Config) {
	chain := config.ChainFromConfig(cfg)
	if len(chain) == 0 {
		return
	}
	primary := chain[0]
	effortHint := ""
	if primary.ReasoningEffort != "" {
		effortHint = ", effort: " + primary.ReasoningEffort
	} else if primary.Temperature != nil {
		effortHint = fmt.Sprintf(", temp: %g", *primary.Temperature)
	}
	fallbackHint := ""
	if len(chain) > 1 {
		s := "s"
		if len(chain) == 2 {
			s = ""
		}
		fallbackHint = fmt.Sprintf(", +%d fallback%s", len(chain)-1, s)
	}
	fmt.Printf("  %s  Config  %s\n", ui.Cyan("✔"), ui.Dim(fmt.Sprintf("(model: %s%s%s)", primary.Model, effortHint, fallbackHint)))
}

func hasFlag(flags []string, matches ...string) bool {
	for _, f := range flags {
		for _, m := range matches {
			if f == m {
				return true
			}
		}
	}
	return false
}

func usage() {
	text := ui.Bold("ignite") + " — EVM security research agent\n\n" +
		"  " + ui.Cyan("ignite") + "                        " + ui.Dim("Run in current directory (auto-detects git)") + "\n" +
		"  " + ui.Cyan("ignite /path/to/repo") + "          " + ui.Dim("Explicit local path") + "\n" +
		"  " + ui.Cyan("ignite --github-url <url>") + "     " + ui.Dim("Clone and audit a public repo") + "\n\n" +
		"  " + ui.Dim("-r  --reconfigure") + "               Reconfigure model / API key\n" +
		"  " + ui.Dim("-u  --update") + "                    Pull latest Docker image before running\n" +
		"  " + ui.Dim("    --fresh") + "                     Force re-clone even if cache exists\n" +
		"  " + ui.Dim("    --skip-build") + "                Skip build phase, re-run analysis only\n" +
		"  " + ui.Dim("-h  --help")

	ui.Panel(text)
}

func handleAbort() {
	fmt.Printf("\n\n  %s  %s\n", ui.Dim("⚠"), ui.Bold("Execution cancelled by user. Exiting..."))
	os.Exit(130)
}
