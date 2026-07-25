package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/touchmeangel/rox_orchestrator/config"
	"github.com/touchmeangel/rox_orchestrator/pkg/agent"
	"github.com/touchmeangel/rox_orchestrator/rabbitworker"
	"github.com/touchmeangel/rox_orchestrator/tasks"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg, err := config.LoadConfig()
	if err != nil {
		logger.Error("config read error", "error", err)
		return
	}

	engine, err := agent.NewEngine(cfg.ListenerAMQPURL, cfg.ListenerQueueName)
	if err != nil {
		logger.Error("client init failed", "error", err)
		return
	}
	defer func() { _ = engine.Close() }()

	w := rabbitworker.New(
		cfg.AMQPURL,
		cfg.QueueName,
		rabbitworker.WithLogger(logger),
		rabbitworker.WithPrefetch(cfg.MaxConcurrentTasks),
	)

	w.On("run", func(ctx context.Context, data json.RawMessage) error {
		return tasks.Run(ctx, engine, data)
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := w.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("worker exited with error", "error", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := w.Close(shutdownCtx); err != nil {
		logger.Error("shutdown error", "error", err)
	}
}
