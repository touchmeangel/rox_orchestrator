package rpc

import (
	"context"

	orchestratorpb "github.com/touchmeangel/rox_proto/rox/orchestrator/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) Run(ctx context.Context, req *orchestratorpb.RunRequest) (*orchestratorpb.RunResponse, error) {
	if req.GetRunId() == "" || req.GetUserId() == "" || req.GetWorkspaceName() == "" {
		return nil, status.Error(codes.InvalidArgument, "run_id, user_id and workspace_name are required")
	}

	select {
	case s.jobs <- req:
		return &orchestratorpb.RunResponse{}, nil
	default:
		return nil, status.Error(codes.ResourceExhausted, "run queue is full, try again later")
	}
}
