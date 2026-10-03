package testsupport

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"buildx/internal/platform/database"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MigratedPostgres creates an isolated schema in the opt-in disposable database.
// Its URL can be passed to code that opens its own connections to the same schema.
func MigratedPostgres(t *testing.T, ctx context.Context) (*pgxpool.Pool, string) {
	t.Helper()
	baseURL := os.Getenv("BUILDX_TEST_DATABASE_URL")
	if baseURL == "" {
		t.Skip("set BUILDX_TEST_DATABASE_URL to a disposable PostgreSQL database")
	}
	root, err := database.Open(ctx, baseURL)
	if err != nil {
		t.Fatalf("open disposable PostgreSQL: %v", err)
	}
	t.Cleanup(root.Close)

	schema := fmt.Sprintf("buildx_test_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := root.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := root.Exec(cleanupCtx, "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Errorf("drop test schema: %v", err)
		}
	})

	parsed, err := url.Parse(baseURL)
	if err != nil {
		t.Fatalf("parse disposable PostgreSQL URL: %v", err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	isolatedURL := parsed.String()
	pool, err := database.Open(ctx, isolatedURL)
	if err != nil {
		t.Fatalf("open test schema: %v", err)
	}
	t.Cleanup(pool.Close)

	// Resolve migrations from this source file so package working directories do not matter.
	_, source, _, _ := runtime.Caller(0)
	files, err := filepath.Glob(filepath.Join(filepath.Dir(source), "..", "..", "db", "migrations", "*.up.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("find application migrations: %v", err)
	}
	for _, file := range files {
		sql, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply %s: %v", file, err)
		}
	}
	return pool, isolatedURL
}
