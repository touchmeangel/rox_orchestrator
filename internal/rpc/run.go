package rpc

import (
	"context"

	runpb "github.com/touchmeangel/rox_proto/rox/run/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func (s *Server) Run(ctx context.Context, req *runpb.RunRequest) (*emptypb.Empty, error) {
	if req.GetRunId() == "" || req.GetRepoPath() == "" {
		return nil, status.Error(codes.InvalidArgument, "run_id and repo_path are required")
	}

	select {
	case s.jobs <- req:
		return &emptypb.Empty{}, nil
	default:
		return nil, status.Error(codes.ResourceExhausted, "run queue is full, try again later")
	}
}
