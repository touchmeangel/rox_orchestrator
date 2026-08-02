package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/touchmeangel/rox_orchestrator/config"
	"github.com/touchmeangel/rox_orchestrator/internal/agent"
	"github.com/touchmeangel/rox_orchestrator/internal/rpc"
	taskpb "github.com/touchmeangel/rox_proto/rox/task/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg, err := config.LoadConfig()
	if err != nil {
		logger.Error("config read error", "error", err)
		return
	}

	engine, err := agent.NewEngine(cfg.ServerAddr)
	if err != nil {
		logger.Error("client init failed", "error", err)
		return
	}
	defer func() { _ = engine.Close() }()

	lis, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		logger.Error("listen failed", "addr", cfg.ListenAddr, "error", err)
		return
	}

	grpcServer := grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    30 * time.Second,
			Timeout: 10 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	)

	srv := rpc.NewServer(engine, logger, cfg.ConcurrentWorkers, cfg.ConcurrentWorkers)
	taskpb.RegisterTaskServiceServer(grpcServer, srv)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("ready to accept tasks", "addr", cfg.ListenAddr, "max_concurrent", cfg.ConcurrentWorkers)
		if err := grpcServer.Serve(lis); err != nil {
			logger.Error("grpc server exited with error", "error", err)
		}
	}()

	<-ctx.Done()
	logger.Info("shutdown signal received, draining in-flight tasks")

	done := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		logger.Warn("graceful stop timed out, forcing shutdown")
		grpcServer.Stop()
	}
}
