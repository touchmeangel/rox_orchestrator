package rpc

import (
	"context"

	orchestratorpb "github.com/touchmeangel/rox_proto/rox/orchestrator/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *Server) Run(ctx context.Context, req *orchestratorpb.RunRequest) (*emptypb.Empty, error) {
	if req.GetRunId() == "" || req.GetWorkspaceName() == "" || req.GetName() == "" || req.GetUserId() == "" {
		return nil, status.Error(codes.InvalidArgument, "run_id, name, user_id and workspace_name are required")
	}

	select {
	case s.jobs <- req:
		return &emptypb.Empty{}, nil
	default:
		return nil, status.Error(codes.ResourceExhausted, "run queue is full, try again later")
	}
}
