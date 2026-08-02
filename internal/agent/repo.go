package agent

import (
	"errors"
	"regexp"
	"strings"

	"github.com/go-git/go-git/v5"
)

var ErrNoRepositoryDetected = errors.New("no git repository discovered at specified destination context")

func (e *Engine) getRepoSpecs(path string) string {
	gitRoot := e.gitRepoRoot(path)
	if gitRoot == "" {
		return "unknown"
	}

	slug := RepoSlug(gitRoot)
	if remoteURL := e.gitRemoteURL(gitRoot); remoteURL != "" {
		slug = RepoSlug(remoteURL)
	}
	return slug
}

func (e *Engine) gitRepoRoot(path string) string {
	repo, err := git.PlainOpenWithOptions(path, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return ""
	}
	wt, err := repo.Worktree()
	if err != nil {
		return ""
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
		return ""
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
