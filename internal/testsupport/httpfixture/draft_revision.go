package httpfixture

import (
	"database/sql"
	"net/url"
	"testing"

	"github.com/pooya79/Piko/internal/platform/database"
)

// Supply an explicitly loaded revision when sending a candidate definition.
func DraftAtRevision(v url.Values, revision string) url.Values {
	v.Set("draft_revision", revision)
	return v
}

// Migration setup stops at an explicit schema version as new migrations are added.
func RollbackToMigration(t *testing.T, db *sql.DB, target string) {
	t.Helper()
	for {
		var latest string
		if err := db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&latest); err != nil {
			t.Fatal(err)
		}
		if latest == target {
			return
		}
		if latest < target {
			t.Fatalf("migration %s is older than target %s", latest, target)
		}
		if err := database.Migrate(t.Context(), db, true); err != nil {
			t.Fatal(err)
		}
	}
}
