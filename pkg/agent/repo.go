package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/touchmeangel/ignite_orchestrator/config"
)

var ErrNoRepositoryDetected = errors.New("no git repository discovered at specified destination context")

func (e *Engine) prepareRepoSpecs(githubURL, path string, force bool) (string, string, error) {
	if githubURL != "" {
		repoPath, err := e.cloneToCache(githubURL, force)
		return repoPath, RepoSlug(githubURL), err
	}

	gitRoot := e.gitRepoRoot(path)
	if gitRoot != "" {
		slug := RepoSlug(gitRoot)
		if remoteURL := e.gitRemoteURL(gitRoot); remoteURL != "" {
			slug = RepoSlug(remoteURL)
		}
		return gitRoot, slug, nil
	}

	return "", "", ErrNoRepositoryDetected
}

func (e *Engine) cloneToCache(githubURL string, force bool) (string, error) {
	slug := RepoSlug(githubURL)
	repoPath := filepath.Join(config.IgniteHome, "repos", slug)

	if info, err := os.Stat(repoPath); err == nil && info.IsDir() {
		if !force {
			return repoPath, nil
		}
		if err := os.RemoveAll(repoPath); err != nil {
			return "", fmt.Errorf("clearing cached checkout for re-clone: %w", err)
		}
	}

	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		return "", fmt.Errorf("failed establishing local caching directory tree structure: %w", err)
	}

	_, err := git.PlainClone(repoPath, false, &git.CloneOptions{
		URL:          githubURL,
		SingleBranch: true,
	})
	if err != nil {
		os.RemoveAll(repoPath)
		return "", fmt.Errorf("git workspace checkout tracking error: %w", err)
	}

	return repoPath, nil
}

func (e *Engine) gitRepoRoot(path string) string {
	repo, err := git.PlainOpenWithOptions(path, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return ""
	}
	wt, err := repo.Worktree()
	if err != nil {
		return "" // bare repo or similar — no worktree to report a root for
	}
	return wt.Filesystem.Root()
}

func (e *Engine) gitRemoteURL(repoRoot string) string {
	repo, err := git.PlainOpen(repoRoot)
	if err != nil {
		return ""
	}
	remote, err := repo.Remote("origin")
	if err != nil {
		return "" // no "origin" configured — not fatal, caller falls back to a path-based slug
	}
	urls := remote.Config().URLs
	if len(urls) == 0 {
		return ""
	}
	return urls[0]
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
