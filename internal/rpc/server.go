package rpc

import (
	"context"
	"log/slog"

	"github.com/touchmeangel/rox_orchestrator/internal/agent"
	"github.com/touchmeangel/rox_orchestrator/internal/tasks"
	orchestratorpb "github.com/touchmeangel/rox_proto/rox/orchestrator/v1"
	taskpb "github.com/touchmeangel/rox_proto/rox/task/v1"
)

type Server struct {
	taskpb.UnimplementedTaskServiceServer
	orchestratorpb.UnimplementedRunServiceServer

	engine *agent.Engine
	logger *slog.Logger
	jobs   chan *orchestratorpb.RunRequest
}

func NewServer(engine *agent.Engine, logger *slog.Logger, queueSize, workers int) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		engine: engine,
		logger: logger,
		jobs:   make(chan *orchestratorpb.RunRequest, queueSize),
	}
	for i := 0; i < workers; i++ {
		go s.worker()
	}
	return s
}

func (s *Server) worker() {
	for req := range s.jobs {
		if err := tasks.Run(context.Background(), s.engine, req); err != nil {
			s.logger.Error("run failed",
				"run_id", req.GetRunId(),
				"workspace", req.GetWorkspaceName(),
				"error", err,
			)
		}
	}
}
