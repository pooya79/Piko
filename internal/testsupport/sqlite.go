package testsupport

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"buildx/internal/platform/database"
)

func MigratedSQLite(t *testing.T, ctx context.Context) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := database.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(ctx, db, false); err != nil {
		t.Fatal(err)
	}
	return db, path
}
