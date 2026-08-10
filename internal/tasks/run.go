package tasks

import (
	"context"

	"github.com/touchmeangel/rox_orchestrator/internal/agent"
	orchestratorpb "github.com/touchmeangel/rox_proto/rox/orchestrator/v1"
)

func Run(ctx context.Context, engine *agent.Engine, req *orchestratorpb.RunRequest) error {
	_, err := engine.Execute(ctx, agent.Options{WorkspaceName: req.GetWorkspaceName()})
	if err != nil {
		return err
	}

	return nil
}
