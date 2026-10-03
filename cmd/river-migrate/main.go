package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"buildx/internal/jobs"
	"buildx/internal/platform/database"
	"buildx/internal/platform/logging"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	log := logging.New(os.Getenv("LOG_LEVEL"))
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	return jobs.Migrate(ctx, pool, log)
}
