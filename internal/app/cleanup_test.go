package app

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/platform/database"
	"github.com/pooya79/Piko/internal/testsupport"
)

func seedCleanupRecords(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users(id,email,display_name,password_hash) VALUES (1,'cleanup@example.test','Mina','unused');
 INSERT INTO sessions(token_hash,user_id,csrf_hash,expires_at) VALUES (x'01',1,x'01',0),(x'02',1,x'02',4102444800000);
 INSERT INTO rate_limits(key,count,expires_at) VALUES ('expired',1,0),('live',1,4102444800000);`); err != nil {
		t.Fatal(err)
	}
}

func assertCleanupRecords(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, table := range []string{"sessions", "rate_limits"} {
		var count, expires int64
		if err := db.QueryRow("SELECT count(*), min(expires_at) FROM "+table).Scan(&count, &expires); err != nil || count != 1 || expires != 4102444800000 {
			t.Fatalf("%s cleanup lost live or retained expired records: count=%d expires=%d err=%v", table, count, expires, err)
		}
	}
}

func TestServerStartupCleansExpiredRecordsBeforeClosingDatabase(t *testing.T) {
	ctx := context.Background()
	db, path := testsupport.MigratedSQLite(t, ctx)
	seedCleanupRecords(t, db)
	a, err := New(ctx, Config{DatabasePath: path, HTTPAddr: "invalid-address", LogLevel: "error", ShutdownPeriod: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Run(ctx); err == nil {
		t.Fatal("invalid listen address accepted")
	}
	if err := a.db.Ping(); err == nil {
		t.Fatal("Run returned before closing database")
	}
	assertCleanupRecords(t, db)
	a, err = New(ctx, Config{DatabasePath: path, HTTPAddr: "invalid-address", LogLevel: "error", ShutdownPeriod: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	_ = a.Run(ctx)
	assertCleanupRecords(t, db)
	if err := database.RequireWAL(ctx, db); err != nil {
		t.Fatal(err)
	}
}

func TestPeriodicCleanupStopsOnCancellation(t *testing.T) {
	db, _ := testsupport.MigratedSQLite(t, t.Context())
	seedCleanupRecords(t, db)
	a := &App{db: db, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ticks := make(chan time.Time, 1)
	done := make(chan struct{})
	go func() { defer close(done); a.cleanupLoop(ctx, ticks) }()
	ticks <- time.Now()
	deadline := time.After(time.Second)
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM sessions WHERE expires_at=0").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("periodic cleanup did not run")
		case <-poll.C:
		}
	}
	assertCleanupRecords(t, db)
	ticks <- time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not stop")
	}
	// The lifecycle can now close storage safely; no further tick can touch it.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case ticks <- time.Now():
	default:
	}
}
