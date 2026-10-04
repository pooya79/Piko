package builder

import (
	"context"
	"database/sql"
	"errors"

	"github.com/pooya79/Piko/internal/bot"
	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/platform/database"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
)

var ErrUndo = errors.New("builder change can no longer be undone")

// Availability and restoration require the same successful-change snapshot;
// restoration also checks the current revision inside its write transaction.
func hasUndoSnapshot(run dbgen.BuilderRun) bool {
	return run.Status == string(RunSucceeded) && run.Result == "saved" && run.BeforeDefinition.Valid && run.AfterDefinition.Valid && run.AfterRevision.Valid
}

// Undo restores a successful turn only at its resulting revision. The shared
// save boundary advances the revision, invalidating repeated and older Undo.
// Draft, run outcome and chat feedback commit together, including across servers.
func (s *Service) Undo(ctx context.Context, botID, chatID, runID int64) error {
	ownerID, err := owner(ctx)
	if err != nil {
		return err
	}
	return database.RetryWrite(ctx, s.repo.db, func(conn *sql.Conn) error {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		q := s.repo.q.WithTx(tx)
		run, err := q.GetOwnerBuilderRun(ctx, dbgen.GetOwnerBuilderRunParams{OwnerID: ownerID, BotID: botID, ChatID: chatID, RunID: runID})
		if err != nil {
			return storageError(err)
		}
		if !hasUndoSnapshot(run) {
			return ErrUndo
		}
		previous, err := flow.Decode(run.BeforeDefinition.String)
		if err != nil {
			return ErrUndo
		}
		if _, err := s.bots.SaveDraftTx(ctx, tx, botID, run.AfterRevision.Int64, previous); errors.Is(err, bot.ErrStaleDraft) {
			return ErrUndo
		} else if err != nil {
			return err
		}
		n, err := q.UndoOwnerBuilderRun(ctx, dbgen.UndoOwnerBuilderRunParams{OwnerID: ownerID, BotID: sql.NullInt64{Int64: botID, Valid: true}, ChatID: sql.NullInt64{Int64: chatID, Valid: true}, RunID: runID})
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrUndo
		}
		if _, err := appendMessage(ctx, q, ownerID, botID, chatID, ResultRole, "builder.run.undone"); err != nil {
			return err
		}
		return tx.Commit()
	})
}
