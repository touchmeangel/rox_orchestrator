package main

import (
	"context"
	"log"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/touchmeangel/rox_models/coordinator"
	"github.com/touchmeangel/rox_models/run"
	"github.com/touchmeangel/rox_models/worker"
	"github.com/touchmeangel/rox_orchestrator/config"
	"github.com/touchmeangel/rox_orchestrator/internal/agent"
	"github.com/touchmeangel/rox_orchestrator/internal/rpc"
	orchestratorpb "github.com/touchmeangel/rox_proto/rox/orchestrator/v1"
	taskpb "github.com/touchmeangel/rox_proto/rox/task/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

func createPool(databaseURL string) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}

	poolCfg.MaxConnLifetime = time.Hour
	poolCfg.MaxConnIdleTime = 30 * time.Minute
	poolCfg.HealthCheckPeriod = time.Minute

	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		return nil, err
	}

	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		return nil, err
	}

	return pool, nil
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg, err := config.LoadConfig()
	if err != nil {
		logger.Error("config read error", "error", err)
		return
	}

	conn, err := grpc.NewClient(cfg.ServerAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()), /// TODO: swap for real TLS creds in production
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                20 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	)
	if err != nil {
		logger.Error("failed to dial task service", "addr", cfg.ServerAddr, "error", err)
		return
	}
	defer func() { _ = conn.Close() }()

	pool, err := createPool(cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()

	runStore := run.NewRunStore(pool)
	workerStore := worker.NewWorkerStore(pool)
	coordinatorStore := coordinator.NewCoordinatorStore(pool)

	engine, err := agent.NewEngine(taskpb.NewTaskServiceClient(conn), runStore, workerStore, coordinatorStore, logger)
	if err != nil {
		logger.Error("client init failed", "error", err)
		return
	}

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
	orchestratorpb.RegisterRunServiceServer(grpcServer, srv)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("ready to work on tasks", "addr", cfg.ListenAddr, "max_concurrent", cfg.ConcurrentWorkers)
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
