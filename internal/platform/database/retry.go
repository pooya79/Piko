package database

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"modernc.org/sqlite"
)

// RetryWrite lets application-owned transactions obey a context deadline even
// under another process's write lock. SQLite's native busy wait does not wake
// promptly on cancellation, so retry lock contention in Go on a pinned connection.
// The normal busy timeout is restored before other users can obtain the connection.
func RetryWrite(ctx context.Context, db *sql.DB, write func(*sql.Conn) error) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "PRAGMA busy_timeout=0"); err != nil {
		return err
	}
	defer func() { _, _ = conn.ExecContext(context.Background(), "PRAGMA busy_timeout=5000") }()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := write(conn)
		var sqliteErr *sqlite.Error
		if !errors.As(err, &sqliteErr) || sqliteErr.Code()&0xff != 5 {
			return err
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
