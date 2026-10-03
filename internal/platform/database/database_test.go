package database_test

import (
	"context"
	"path/filepath"
	"testing"

	"buildx/internal/platform/database"
	"buildx/internal/testsupport"
)

func TestMigrationLifecycleAndSeed(t *testing.T) {
	ctx := context.Background()
	db, _ := testsupport.MigratedSQLite(t, ctx)
	if err := database.Migrate(ctx, db, false); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := database.Seed(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM users").Scan(&count); err != nil || count != 1 {
		t.Fatalf("seed count = %d, error = %v", count, err)
	}
	if err := database.Migrate(ctx, db, true); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM users").Scan(&count); err == nil {
		t.Fatal("rollback retained users")
	}
	if err := database.Migrate(ctx, db, false); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionsPreserveWALAndEnforceForeignKeys(t *testing.T) {
	ctx := context.Background()
	db, path := testsupport.MigratedSQLite(t, ctx)
	other, err := database.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	var mode string
	if err := other.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal mode = %q, error = %v", mode, err)
	}
	if _, err := other.ExecContext(ctx, "INSERT INTO sessions(token_hash, user_id, csrf_hash, expires_at) VALUES (x'01', 999, x'02', 0)"); err == nil {
		t.Fatal("missing foreign key accepted")
	}
	if err := database.Seed(ctx, db); err != nil {
		t.Fatal(err)
	}
	var firstID int64
	if err := db.QueryRowContext(ctx, "SELECT id FROM users").Scan(&firstID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "INSERT INTO sessions(token_hash, user_id, csrf_hash, expires_at) SELECT x'01', id, x'02', 0 FROM users"); err != nil {
		t.Fatal(err)
	}
	if _, err := other.ExecContext(ctx, "DELETE FROM users"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sessions").Scan(&count); err != nil || count != 0 {
		t.Fatalf("cascade count = %d, error = %v", count, err)
	}
	if err := database.Seed(ctx, db); err != nil {
		t.Fatal(err)
	}
	var nextID int64
	if err := db.QueryRowContext(ctx, "SELECT id FROM users").Scan(&nextID); err != nil {
		t.Fatal(err)
	}
	if nextID <= firstID {
		t.Fatal("deleted account identity was reused")
	}
}

func TestMigrationInitializesPersistentWAL(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := database.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.RequireWAL(ctx, db); err == nil {
		t.Fatal("opening a fresh database enabled WAL before migrations")
	}
	if err := database.Migrate(ctx, db, false); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := database.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if err := database.RequireWAL(ctx, reopened); err != nil {
		t.Fatalf("migration WAL setting did not persist: %v", err)
	}
	// A startup check must report disabled WAL without silently enabling it.
	if _, err := reopened.ExecContext(ctx, "PRAGMA journal_mode=DELETE"); err != nil {
		t.Fatal(err)
	}
	if err := database.RequireWAL(ctx, reopened); err == nil {
		t.Fatal("startup accepted disabled WAL")
	}
	var mode string
	if err := reopened.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "delete" {
		t.Fatalf("startup check changed journal mode: %q, %v", mode, err)
	}
	// Re-running migrations can repair the mode without reapplying schema SQL.
	if err := database.Migrate(ctx, reopened, false); err != nil {
		t.Fatal(err)
	}
	if err := database.RequireWAL(ctx, reopened); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRejectsNonFileConfiguration(t *testing.T) {
	for _, path := range []string{"", "postgres://localhost/test", ":memory:", "file:test.db"} {
		if db, err := database.Open(context.Background(), path); err == nil {
			_ = db.Close()
			t.Fatalf("accepted %q", path)
		}
	}
	db, err := database.Open(context.Background(), filepath.Join(t.TempDir(), "nested", "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
}
