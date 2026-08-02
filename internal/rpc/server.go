package rpc

import (
	"context"
	"log/slog"

	"github.com/touchmeangel/rox_orchestrator/internal/agent"
	"github.com/touchmeangel/rox_orchestrator/internal/tasks"
	runpb "github.com/touchmeangel/rox_proto/rox/run/v1"
	taskpb "github.com/touchmeangel/rox_proto/rox/task/v1"
)

type Server struct {
	taskpb.UnimplementedTaskServiceServer
	runpb.UnimplementedRunServiceServer

	engine *agent.Engine
	logger *slog.Logger
	jobs   chan *runpb.RunRequest
}

func NewServer(engine *agent.Engine, logger *slog.Logger, queueSize, workers int) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		engine: engine,
		logger: logger,
		jobs:   make(chan *runpb.RunRequest, queueSize),
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
				"repo_path", req.GetRepoPath(),
				"error", err,
			)
		}
	}
}
