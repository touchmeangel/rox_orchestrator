package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/touchmeangel/ignite/config"
	"github.com/touchmeangel/ignite/dockerx"
	"github.com/touchmeangel/ignite/ui"
)

const defaultImage = "touchmeangel/ignite_agent:latest"

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
			fmt.Println("  " + ui.Red("✗") + "  Missing value for " + a + ".")
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
	absPath, err := filepath.Abs(inspectPath)
	if err != nil || !exists(absPath) {
		fmt.Printf("  %s  Path not found: %s\n", ui.Red("✗"), absPath)
		os.Exit(1)
	}

	debugPath := filepath.Join(config.IgniteHome, "debug.log")
	configPath := config.ConfigPath()

	fmt.Printf("  %s %s\n", ui.Dim("➔ Debug:"), ui.Cyan(debugPath))
	fmt.Printf("  %s %s\n\n", ui.Dim("➔ Pipeline:"), ui.Cyan("Foundry"))

	var cfg *config.Config

	if config.Exists() && !reconfigure {
		cfg, err = config.Load()
		if err == nil {
			chain := config.ChainFromConfig(cfg)
			if len(chain) > 0 {
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
		}
	} else if reconfigure {
		fmt.Printf("  %s  Using Foundry pipeline\n", ui.Cyan("✔"))
		if !dockerRunning() {
			fmt.Println("  " + ui.Red("✗") + "  Docker is not running. Start Docker Desktop and retry.")
			os.Exit(1)
		}
		fmt.Printf("  %s  Docker available\n", ui.Cyan("✔"))
		cfg, _ = config.RunReconfigure()
	} else {
		fmt.Println("  " + ui.Dim("No config found — running first-time setup."))
		fmt.Printf("  %s  Using Foundry pipeline\n", ui.Cyan("✔"))
		if !dockerRunning() {
			fmt.Println("  " + ui.Red("✗") + "  Docker is not running. Start Docker Desktop and retry.")
			os.Exit(1)
		}
		fmt.Printf("  %s  Docker available\n", ui.Cyan("✔"))
		cfg, _ = config.RunSetup()
	}

	if !dockerRunning() {
		fmt.Println("  " + ui.Red("✗") + "  Docker is not running. Start Docker Desktop and retry.")
		os.Exit(1)
	}

	if doUpdate {
		pullImage()
	}

	repoPath, slug := prepareRepo(githubURL, absPath, forceReclone)

	workPath := filepath.Join(config.IgniteHome, "workspaces", slug)
	os.MkdirAll(workPath, 0o755)
	fmt.Printf("  %s  Workspace  %s\n", ui.Cyan("✔"), ui.Dim(workPath))

	if skipBuild {
		fmt.Printf("  %s  %s\n", ui.Yellow("⚠"), ui.Dim("--skip-build: build phase will be skipped"))
	}

	ui.Rule("")
	fmt.Printf("\n  Running agent  %s\n\n", ui.Dim(repoPath))

	rc := runContainer(repoPath, workPath, configPath, debugPath, skipBuild, cfg)
	if rc != 0 {
		fmt.Printf("\n  %s %s\n", ui.Red("✗"), ui.Red(ui.Bold(fmt.Sprintf("Container execution failed (exit code %d).", rc))))
		os.Exit(rc)
	}

	resultsPath := filepath.Join(workPath, "agent_results.json")
	completionText := ui.BoldCyan("AUDIT ENGINE PIPELINE COMPLETE\n\n") +
		ui.Dim("Status:     ") + ui.Bold("Active / Success\n") +
		ui.Dim("Artifacts:  ") + ui.Cyan(filepath.Base(resultsPath)+"\n") +
		ui.Dim("Location:   ") + ui.Dim(workPath)

	ui.Panel(completionText)
	fmt.Println()
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

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func dockerRunning() bool {
	cli, err := dockerx.New()
	if err != nil {
		return false
	}
	defer cli.Close()

	ctx := context.Background()

	return cli.Ping(ctx)
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

func pullImage() {
	fmt.Printf("  %s\n", ui.Dim("Pulling latest image: "+defaultImage+" …"))

	cli, err := dockerx.New()
	if err != nil {
		fmt.Printf("  %s  Failed to connect to Docker SDK: %v\n", ui.Red("✗"), err)
		os.Exit(1)
	}
	defer cli.Close()

	if err := cli.PullImage(context.Background(), defaultImage); err != nil {
		fmt.Printf("  %s  Docker SDK image pull failed: %v\n", ui.Red("✗"), err)
		os.Exit(1)
	}

	fmt.Printf("  %s  Image updated.\n", ui.Cyan("✔"))
}

func runContainer(repoPath, workPath, configPath, debugPath string, skipBuild bool, cfg *config.Config) int {
	cli, err := dockerx.New()
	if err != nil {
		fmt.Println(ui.Red("✗") + " Docker client error: " + err.Error())
		return 1
	}
	defer cli.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	envMap := config.LoadEnvVars(cfg)
	var env []string
	for k, v := range envMap {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	cmdArgs := []string{"--repo-path", "/repo", "--work-path", "/work", "--output", "/work/agent_results.json", "--debug", "/app/debug.log"}
	if skipBuild {
		cmdArgs = append(cmdArgs, "--skip-build")
	}

	spinner := ui.NewSpinner("Initializing container...")
	spinner.Start()

	go func() {
		spinner.Stop()
	}()

	spec := dockerx.RunSpec{
		Image: defaultImage,
		Name:  "ignite-agent",
		Cmd:   cmdArgs,
		Env:   env,
		Mounts: []dockerx.Mount{
			{Source: repoPath, Target: "/repo", ReadOnly: true},
			{Source: workPath, Target: "/work", ReadOnly: false},
			{Source: configPath, Target: "/app/config.json", ReadOnly: true},
			{Source: debugPath, Target: "/app/debug.log", ReadOnly: false},
		},
		LogPrefix: "foundry",
	}

	code, err := cli.Run(ctx, spec)
	if err != nil {
		return 1
	}
	return int(code)
}

func prepareRepo(githubURL string, path string, forceReclone bool) (string, string) {
	if githubURL != "" {
		repoPath := cloneToCache(githubURL, forceReclone)
		return repoPath, repoSlug(githubURL)
	}

	gitRoot := gitRepoRoot(path)
	if gitRoot != "" {
		remoteURL := gitRemoteURL(gitRoot)
		display := remoteURL
		if display == "" {
			display = gitRoot
		}
		fmt.Printf("  %s  Git repo  %s\n", ui.Cyan("✔"), ui.Dim(display))

		var slug string
		if remoteURL != "" {
			slug = repoSlug(remoteURL)
		} else {
			slug = repoSlug(gitRoot)
		}
		return gitRoot, slug
	}

	fmt.Printf("  %s  No git repository found at %s\n", ui.Dim("⚠"), path)

	choices := []string{"Use this directory as source", "Enter a GitHub URL to clone"}
	choice, err := ui.Select("How would you like to proceed?", choices, 0)
	if err != nil {
		handleAbort()
	}

	if choice == 1 { // "Enter a GitHub URL to clone"
		entered, err := ui.Text("GitHub URL", "")
		if err != nil || entered == "" {
			handleAbort()
		}
		entered = strings.TrimSpace(entered)
		repoPath := cloneToCache(entered, forceReclone)
		return repoPath, repoSlug(entered)
	}

	slug := repoSlug(path)
	fmt.Printf("  %s  Using directory as-is  %s\n", ui.Cyan("✔"), ui.Dim(path))
	return path, slug
}

func cloneToCache(githubURL string, force bool) string {
	slug := repoSlug(githubURL)
	repoPath := filepath.Join(config.IgniteHome, "repos", slug)

	if exists(repoPath) && !force {
		fmt.Printf("  %s  Cached clone  %s  %s\n", ui.Cyan("✔"), ui.Dim(repoPath), ui.Dim("(--fresh to re-clone)"))
		return repoPath
	}

	if exists(repoPath) && force {
		os.RemoveAll(repoPath)
	}

	os.MkdirAll(repoPath, 0o755)
	fmt.Printf("  %s\n", ui.Dim("Cloning "+githubURL+" …"))

	cmd := exec.Command("git", "clone", "--depth", "1", githubURL, repoPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		os.RemoveAll(repoPath)
		fmt.Printf("  %s  git clone failed:\n%s\n", ui.Red("✗"), strings.TrimSpace(stderr.String()))
		os.Exit(1)
	}

	fmt.Printf("  %s  Cloned  %s\n", ui.Cyan("✔"), ui.Dim(repoPath))
	return repoPath
}

func gitRepoRoot(path string) string {
	if _, err := exec.LookPath("git"); err != nil {
		return ""
	}
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = path
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if cmd.Run() == nil {
		return strings.TrimSpace(stdout.String())
	}
	return ""
}

func gitRemoteURL(repoRoot string) string {
	if _, err := exec.LookPath("git"); err != nil {
		return ""
	}
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = repoRoot
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if cmd.Run() == nil {
		return strings.TrimSpace(stdout.String())
	}
	return ""
}

func repoSlug(source string) string {
	source = strings.TrimSuffix(source, "/")
	source = strings.TrimSuffix(source, ".git")

	normalized := strings.ReplaceAll(source, "\\", "/")
	parts := strings.Split(normalized, "/")

	var slug string
	isHost := false
	for _, host := range []string{"github.com", "gitlab.com", "bitbucket.org"} {
		if strings.Contains(source, host) {
			isHost = true
			break
		}
	}

	if isHost && len(parts) >= 2 {
		slug = parts[len(parts)-2] + "_" + parts[len(parts)-1]
	} else if len(parts) > 0 {
		slug = parts[len(parts)-1]
	}

	if slug == "" {
		slug = "repo"
	}

	reg := regexp.MustCompile(`[^a-zA-Z0-9_-]`)
	slug = reg.ReplaceAllString(slug, "_")

	return slug
}

func handleAbort() {
	fmt.Printf("\n\n  %s  %s\n", ui.Dim("⚠"), ui.Bold("Execution cancelled by user. Exiting..."))
	os.Exit(130)
}
