package tasks

import (
	"context"

	"github.com/touchmeangel/rox_orchestrator/internal/agent"
	runpb "github.com/touchmeangel/rox_proto/rox/run/v1"
)

func Run(ctx context.Context, engine *agent.Engine, req *runpb.RunRequest) error {
	_, err := engine.Execute(ctx, agent.Options{RepoPath: req.RepoPath})
	if err != nil {
		return err
	}

	return nil
}
