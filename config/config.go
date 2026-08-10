package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ServerAddr        string
	ListenAddr        string
	DatabaseURL       string
	ConcurrentWorkers int
}

func LoadConfig() (Config, error) {
	cfg := Config{
		ServerAddr:  os.Getenv("SERVER_ADDRESS"),
		ListenAddr:  os.Getenv("LISTEN_ADDRESS"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
	}

	var missing []string
	required := map[string]string{
		"SERVER_ADDRESS": cfg.ServerAddr,
		"LISTEN_ADDRESS": cfg.ListenAddr,
		"DATABASE_URL":   cfg.DatabaseURL,
	}
	for name, val := range required {
		if val == "" {
			missing = append(missing, name)
		}
	}

	raw := os.Getenv("MAX_CONCURRENT_TASKS")
	if raw == "" {
		missing = append(missing, "MAX_CONCURRENT_TASKS")
	} else if n, err := strconv.Atoi(raw); err != nil || n <= 0 {
		return cfg, fmt.Errorf("MAX_CONCURRENT_TASKS must be a positive integer, got %q", raw)
	} else {
		cfg.ConcurrentWorkers = n
	}

	if len(missing) > 0 {
		return cfg, fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return cfg, nil
}
