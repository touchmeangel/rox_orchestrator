package tasks

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/touchmeangel/rox_orchestrator/pkg/agent"
	"github.com/touchmeangel/rox_orchestrator/rabbitworker"
)

type RunTask struct {
	RunID    string `json:"run_id"`
	RepoPath string `json:"repo_path"`
}

func Run(ctx context.Context, engine *agent.Engine, data json.RawMessage) error {
	var task RunTask
	if err := json.Unmarshal(data, &task); err != nil {
		return rabbitworker.Permanent(fmt.Errorf("invalid run task payload: %w", err))
	}

	_, err := engine.Execute(ctx, agent.Options{RepoPath: task.RepoPath})
	if err != nil {
		return err
	}

	return nil
}
