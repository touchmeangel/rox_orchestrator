package agent

import (
	"context"

	"github.com/touchmeangel/rox_orchestrator/dockerx"
)

type Runner interface {
	Run(ctx context.Context, spec dockerx.RunSpec) (int64, error)
}
