package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"buildx/internal/platform/database"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 2 && args[0] == "create" {
		return create(args[1])
	}
	if len(args) != 1 || (args[0] != "up" && args[0] != "down" && args[0] != "seed") {
		return errors.New("usage: buildx-migrate up|down|seed|create name")
	}
	if args[0] == "seed" && os.Getenv("APP_ENV") != "development" {
		return errors.New("seeding requires APP_ENV=development")
	}
	db, err := database.Open(ctx, os.Getenv("DATABASE_PATH"))
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	if args[0] == "seed" {
		return database.Seed(ctx, db)
	}
	return database.Migrate(ctx, db, args[0] == "down")
}

func create(name string) error {
	if !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(name) {
		return errors.New("migration name must contain lowercase letters, digits, and underscores")
	}
	files, err := filepath.Glob("db/migrations/*.up.sql")
	if err != nil {
		return err
	}
	next := 1
	for _, file := range files {
		prefix, _, _ := strings.Cut(filepath.Base(file), "_")
		n, err := strconv.Atoi(prefix)
		if err != nil {
			return err
		}
		next = max(next, n+1)
	}
	for _, direction := range []string{"up", "down"} {
		path := fmt.Sprintf("db/migrations/%06d_%s.%s.sql", next, name, direction)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err != nil {
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
	}
	return nil
}
