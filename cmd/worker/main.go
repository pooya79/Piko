package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"buildx/internal/worker"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := worker.LoadConfig()
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	w, err := worker.New(ctx, cfg)
	if err != nil {
		return err
	}
	return w.Run(ctx)
}
