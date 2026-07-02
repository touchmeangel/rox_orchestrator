package agent

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/touchmeangel/ignite/config"
)

var ErrNoRepositoryDetected = errors.New("no git repository discovered at specified destination context")

func (e *Engine) prepareRepoSpecs(githubURL, path string, force bool) (string, string, error) {
	if githubURL != "" {
		repoPath, err := e.cloneToCache(githubURL, force)
		return repoPath, RepoSlug(githubURL), err
	}

	gitRoot := e.gitRepoRoot(path)
	if gitRoot != "" {
		remoteURL := e.gitRemoteURL(gitRoot)
		var slug string
		if remoteURL != "" {
			slug = RepoSlug(remoteURL)
		} else {
			slug = RepoSlug(gitRoot)
		}
		return gitRoot, slug, nil
	}

	return "", "", ErrNoRepositoryDetected
}

func (e *Engine) cloneToCache(githubURL string, force bool) (string, error) {
	slug := RepoSlug(githubURL)
	repoPath := filepath.Join(config.IgniteHome, "repos", slug)

	_, err := os.Stat(repoPath)
	exists := err == nil

	if exists && !force {
		return repoPath, nil
	}

	if exists && force {
		os.RemoveAll(repoPath)
	}

	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		return "", fmt.Errorf("failed establishing local caching directory tree structure: %w", err)
	}

	cmd := exec.Command("git", "clone", "--depth", "1", githubURL, repoPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		os.RemoveAll(repoPath)
		return "", fmt.Errorf("git workspace checkout tracking error: %s", strings.TrimSpace(stderr.String()))
	}

	return repoPath, nil
}

func (e *Engine) gitRepoRoot(path string) string {
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

func (e *Engine) gitRemoteURL(repoRoot string) string {
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

func RepoSlug(source string) string {
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
