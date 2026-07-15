package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/touchmeangel/ignite_orchestrator/config"
	"github.com/touchmeangel/ignite_orchestrator/pkg/agent"
	"github.com/touchmeangel/ignite_orchestrator/ui"
)

var spinnerFrames = []string{
	"·",
	"✦",
	"✶",
	"✳",
	"✴",
	"❋",
	"❆",
	"❅",
	"❄",
	"❅",
	"❆",
	"❋",
	"✴",
	"✳",
	"✶",
	"✦",
}

func main() {
	args := os.Args[1:]
	var githubURL string
	var concurrencyFlag string
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
		if a == "--concurrency" || a == "-c" {
			if i+1 < len(args) {
				concurrencyFlag = args[i+1]
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
	defer func() { _ = coreEngine.Close() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err = coreEngine.VerifyDaemonIsRunning(ctx); err != nil {
		fmt.Printf("  %s  Docker ping failed: %v\n", ui.Red("✗"), err)
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
		fmt.Printf("  %s  Images updated.", ui.Cyan("✔"))
	}

	opts := agent.Options{
		GithubURL:         githubURL,
		InspectPath:       inspectPath,
		ForceFresh:        forceReclone,
		SkipBuild:         skipBuild,
		Config:            cfg,
		WorkerConcurrency: resolveWorkerConcurrency(concurrencyFlag),
	}

	if skipBuild {
		fmt.Printf("  %s  %s\n", ui.Yellow("⚠"), ui.Dim("--skip-build: build phase will be skipped"))
	}

	fmt.Println()

	region := ui.NewLiveRegion()
	coreEngine.SetLive(region)
	res, err := runWithLiveStatus(ctx, coreEngine, opts, region)

	if errors.Is(err, agent.ErrNoRepositoryDetected) {
		res, err = handleInteractivePathResolution(ctx, coreEngine, inspectPath, opts, region)
	}

	if err != nil {
		fmt.Printf("\n  %s %s\n", ui.Red("✗"), ui.Red(fmt.Sprintf("Pipeline engine aborted: %v", err)))
		os.Exit(1)
	}

	succeeded, failed := 0, 0
	for _, w := range res.Workers {
		if w.Err == nil && w.ExitCode == 0 {
			succeeded++
		} else {
			failed++
		}
	}

	if failed > 0 {
		fmt.Printf("  %s %s\n", ui.Red("✗"), ui.Red(ui.Bold("Container execution failed.")))
		printWorkerFailures(res.Workers)
	}

	artifactsLabel := "N/A"
	if res.ResultsFile != "" {
		artifactsLabel = filepath.Base(res.ResultsFile)
	}

	completionText := ui.BoldCyan("AGENT PIPELINE COMPLETE\n\n") +
		ui.Dim("Workers:    ") + ui.Bold(fmt.Sprintf("%d succeeded, %d failed\n", succeeded, failed)) +
		ui.Dim("Artifacts:  ") + ui.Cyan(artifactsLabel+"\n")

	fmt.Println(ui.Panel(completionText))
	fmt.Println()

	printModelUsage(res.UsageSummary)

	if res.ExitCode != 0 {
		os.Exit(res.ExitCode)
	}
}

const defaultWorkerConcurrency = 4

func resolveWorkerConcurrency(flagValue string) int {
	if flagValue != "" {
		if n, err := strconv.Atoi(flagValue); err == nil && n > 0 {
			return n
		}
		fmt.Printf("  %s  Invalid --concurrency value %q, falling back to default.\n", ui.Yellow("⚠"), flagValue)
	}
	if raw := os.Getenv("WORKER_CONCURRENCY"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
		fmt.Printf("  %s  Invalid WORKER_CONCURRENCY=%q, falling back to default.\n", ui.Yellow("⚠"), raw)
	}
	return defaultWorkerConcurrency
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
		return "Running coordinator…"
	case agent.PHASE_LOADING_MISSIONS:
		return "Loading missions…"
	case agent.PHASE_WORKING:
		return "Starting workers…"
	default:
		return "Working…"
	}
}

func runWithLiveStatus(ctx context.Context, e *agent.Engine, opts agent.Options, region *ui.LiveRegion) (*agent.Result, error) {
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
	region.SetStatus(fmt.Sprintf("  %s %s", ui.Cyan(spinnerFrames[frame]), ui.Dim("Initializing…")))

	for {
		select {
		case out := <-done:
			region.Clear()
			return out.res, out.err
		case <-ticker.C:
			frame = (frame + 1) % len(spinnerFrames)

			var label string
			active := e.ActiveWorkers()
			queued := e.QueuedWorkers()

			if active > 0 || queued > 0 {
				activePlural := "s"
				if active == 1 {
					activePlural = ""
				}
				if queued > 0 {
					queuedPlural := "s"
					if queued == 1 {
						queuedPlural = ""
					}
					label = fmt.Sprintf("%d worker%s active, %d queued worker%s", active, activePlural, queued, queuedPlural)
				} else {
					label = fmt.Sprintf("%d worker%s active", active, activePlural)
				}
			} else {
				label = phaseToString(e.Phase())
			}

			region.SetStatus(fmt.Sprintf("  %s %s", ui.Cyan(spinnerFrames[frame]), ui.Dim(label)))
		}
	}
}

func printModelUsage(usage *agent.UsageSummary) {
	if usage == nil || len(usage.ByModel) == 0 {
		return
	}

	keys := make([]string, 0, len(usage.ByModel))
	for k := range usage.ByModel {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	fmt.Printf("  %s\n", ui.Bold("Model usage:"))
	for _, key := range keys {
		e := usage.ByModel[key]
		label := usageLabel(key, e.Model)
		fmt.Printf(
			"    %-40s %s  %s\n",
			label,
			ui.Dim(fmt.Sprintf("%5.1f%% calls", e.PctOfCalls)),
			ui.Dim(fmt.Sprintf("%5.1f%% tokens", e.PctOfTokens)),
		)
	}
	fmt.Println()
}

func usageLabel(key, model string) string {
	parts := strings.SplitN(key, ":", 3)
	if len(parts) < 2 {
		return key
	}
	account := parts[1]
	if account == "" || account == "null" {
		return model
	}
	return fmt.Sprintf("%s (%s)", model, account)
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

func handleInteractivePathResolution(ctx context.Context, e *agent.Engine, originalPath string, opts agent.Options, region *ui.LiveRegion) (*agent.Result, error) {
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
	return runWithLiveStatus(ctx, e, opts, region)
}

func renderConfigMetadata(cfg *config.Config) {
	chain := config.ChainFromConfig(cfg)
	numModels := len(chain)

	if numModels == 0 {
		return
	}

	var details string

	if numModels == 1 {
		m := chain[0]
		effortHint := ""
		if m.ReasoningEffort != "" {
			effortHint = ", effort: " + m.ReasoningEffort
		} else if m.Temperature != nil {
			effortHint = fmt.Sprintf(", temp: %g", *m.Temperature)
		}
		details = fmt.Sprintf("(model: %s%s)", m.Model, effortHint)
	} else {
		details = fmt.Sprintf("(pool of %d models)", numModels)
	}

	fmt.Printf("  %s  Config  %s\n", ui.Cyan("✔"), ui.Dim(details))
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
