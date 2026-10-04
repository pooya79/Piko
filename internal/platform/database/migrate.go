package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	source "github.com/pooya79/Piko/db"
)

// Migrate applies versioned migrations atomically and serializes concurrent migrators.
func Migrate(ctx context.Context, db *sql.DB, down bool) (result error) {
	if !down {
		if err := initializeWAL(ctx, db); err != nil {
			return err
		}
	}
	// SQLite's table-rebuild procedure must disable FK enforcement before the
	// transaction. Pin one connection so this setting cannot affect app traffic.
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		return err
	}
	defer func() {
		restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(restoreCtx, "PRAGMA foreign_keys=ON"); err != nil {
			// Never return a connection with disabled enforcement to the pool.
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
			result = errors.Join(result, fmt.Errorf("restore foreign keys: %w", err))
		}
	}()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY)"); err != nil {
		return err
	}
	files, err := fs.Glob(source.Files, "migrations/*.up.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)
	if down {
		var version string
		err := tx.QueryRowContext(ctx, "SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1").Scan(&version)
		if errors.Is(err, sql.ErrNoRows) {
			return tx.Commit()
		}
		if err != nil {
			return err
		}
		body, err := source.Files.ReadFile("migrations/" + version + ".down.sql")
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM schema_migrations WHERE version = ?", version); err != nil {
			return err
		}
	} else {
		for _, file := range files {
			version := strings.TrimSuffix(strings.TrimPrefix(file, "migrations/"), ".up.sql")
			var count int
			if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations WHERE version = ?", version).Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				continue
			}
			body, err := source.Files.ReadFile(file)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, string(body)); err != nil {
				return fmt.Errorf("apply %s: %w", file, err)
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations(version) VALUES (?)", version); err != nil {
				return err
			}
		}
	}
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	invalid := rows.Next()
	checkErr := rows.Err()
	closeErr := rows.Close()
	if invalid {
		return errors.New("migration would violate foreign keys")
	}
	if err := errors.Join(checkErr, closeErr); err != nil {
		return err
	}
	return tx.Commit()
}

// WAL persists in the database file. Enable it before the first schema migration:
// SQLite cannot change journal mode inside the migration transaction.
func initializeWAL(ctx context.Context, db *sql.DB) error {
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("read database journal mode: %w", err)
	}
	if mode == "wal" {
		return nil
	}
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		return fmt.Errorf("enable WAL before migrations: %w", err)
	}
	if mode != "wal" {
		return fmt.Errorf("could not enable WAL before migrations: journal mode is %q", mode)
	}
	return nil
}

func Seed(ctx context.Context, db *sql.DB) error {
	body, err := source.Files.ReadFile("seeds/development.sql")
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, string(body))
	return err
}
