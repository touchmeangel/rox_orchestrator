package dockerx

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types/registry"
)

type dockerConfigFile struct {
	Auths map[string]struct {
		Auth string `json:"auth"`
	} `json:"auths"`
	CredsStore  string            `json:"credsStore"`
	CredHelpers map[string]string `json:"credHelpers"`
}

func loadDockerConfig() (*dockerConfigFile, error) {
	dir := os.Getenv("DOCKER_CONFIG")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(home, ".docker")
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return nil, err
	}
	var cf dockerConfigFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filepath.Join(dir, "config.json"), err)
	}
	return &cf, nil
}

func registryHost(ref string) string {
	parts := strings.SplitN(ref, "/", 2)
	if len(parts) == 2 && (strings.ContainsAny(parts[0], ".:") || parts[0] == "localhost") {
		return parts[0]
	}
	return "https://index.docker.io/v1/"
}

type credHelperOutput struct {
	ServerURL string `json:"ServerURL"`
	Username  string `json:"Username"`
	Secret    string `json:"Secret"`
}

func runCredHelper(store, host string) (username, secret string, err error) {
	if store == "" {
		return "", "", fmt.Errorf("no credential store configured")
	}
	cmd := exec.Command("docker-credential-"+store, "get")
	cmd.Stdin = strings.NewReader(host)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", "", fmt.Errorf("docker-credential-%s get %s: %w (%s)", store, host, err, strings.TrimSpace(stderr.String()))
	}
	var res credHelperOutput
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		return "", "", fmt.Errorf("parsing docker-credential-%s output: %w", store, err)
	}
	return res.Username, res.Secret, nil
}

func registryAuth(ref string) (string, error) {
	cf, err := loadDockerConfig()
	if err != nil {
		return "", nil
	}
	host := registryHost(ref)

	var username, secret string

	switch {
	case cf.CredHelpers[host] != "":
		username, secret, err = runCredHelper(cf.CredHelpers[host], host)
	case cf.CredsStore != "":
		username, secret, err = runCredHelper(cf.CredsStore, host)
	default:
		if entry, ok := cf.Auths[host]; ok && entry.Auth != "" {
			decoded, decErr := base64.StdEncoding.DecodeString(entry.Auth)
			if decErr != nil {
				return "", fmt.Errorf("decoding stored auth for %s: %w", host, decErr)
			}
			if u, p, found := strings.Cut(string(decoded), ":"); found {
				username, secret = u, p
			}
		}
	}
	if err != nil {
		return "", err
	}
	if username == "" && secret == "" {
		return "", nil
	}

	encoded, err := json.Marshal(registry.AuthConfig{
		Username:      username,
		Password:      secret,
		ServerAddress: host,
	})
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(encoded), nil
}
