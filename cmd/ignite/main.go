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

	"github.com/touchmeangel/ignite/config"
	"github.com/touchmeangel/ignite/pkg/agent"
	"github.com/touchmeangel/ignite/ui"
)

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

	debugPath := filepath.Join(config.IgniteHome, "debug.log")
	fmt.Printf("  %s %s\n", ui.Dim("➔ Debug:"), ui.Cyan(debugPath))
	fmt.Printf("  %s %s\n\n", ui.Dim("➔ Pipeline:"), ui.Cyan("Foundry"))

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
			fmt.Printf("  %s  Using Foundry pipeline\n", ui.Cyan("✔"))
			fmt.Printf("  %s  Docker available\n", ui.Cyan("✔"))
			cfg, _ = config.RunReconfigure()
		} else {
			fmt.Println("  " + ui.Dim("No config found — running first-time setup."))
			fmt.Printf("  %s  Using Foundry pipeline\n", ui.Cyan("✔"))
			fmt.Printf("  %s  Docker available\n", ui.Cyan("✔"))
			cfg, _ = config.RunSetup()
		}
	}

	if doUpdate {
		fmt.Printf("  %s\n", ui.Dim("Pulling latest core image framework execution model layer..."))
		if err := coreEngine.SyncImage(ctx); err != nil {
			fmt.Printf("  %s Synchronization image update tracking error: %v\n", ui.Red("✗"), err)
			os.Exit(1)
		}
		fmt.Printf("  %s  Image updated.\n", ui.Cyan("✔"))
	}

	opts := agent.Options{
		GithubURL:   githubURL,
		InspectPath: inspectPath,
		ForceFresh:  forceReclone,
		SkipBuild:   skipBuild,
		Config:      cfg,
	}

	if skipBuild {
		fmt.Printf("  %s  %s\n", ui.Yellow("⚠"), ui.Dim("--skip-build: build phase will be skipped"))
	}

	ui.Rule("")
	spinner := ui.NewSpinner("Running core engine analysis processing sequence...")
	spinner.Start()

	res, err := coreEngine.Execute(ctx, opts)
	spinner.Stop()

	if errors.Is(err, agent.ErrNoRepositoryDetected) {
		res, err = handleInteractivePathResolution(ctx, coreEngine, inspectPath, opts)
	}

	if err != nil {
		fmt.Printf("\n  %s %s\n", ui.Red("✗"), ui.Red(fmt.Sprintf("Pipeline engine aborted: %v", err)))
		os.Exit(1)
	}

	if res.ExitCode != 0 {
		fmt.Printf("\n  %s %s\n", ui.Red("✗"), ui.Red(ui.Bold(fmt.Sprintf("Container execution failed (exit code %d).", res.ExitCode))))
		os.Exit(res.ExitCode)
	}

	completionText := ui.BoldCyan("AUDIT ENGINE PIPELINE COMPLETE\n\n") +
		ui.Dim("Status:     ") + ui.Bold("Active / Success\n") +
		ui.Dim("Artifacts:  ") + ui.Cyan(filepath.Base(res.ResultsFile)+"\n") +
		ui.Dim("Location:   ") + ui.Dim(res.WorkspacePath)

	ui.Panel(completionText)
	fmt.Println()
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
	return e.Execute(ctx, opts)
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
