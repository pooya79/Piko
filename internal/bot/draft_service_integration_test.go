package bot_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot"
	"github.com/pooya79/Piko/internal/bot/flow"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func saveDraftCandidate(ctx context.Context, db *sql.DB, service *bot.Service, expected int64, candidate flow.Definition) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	revision, err := service.SaveDraftTx(ctx, tx, 1, expected, candidate)
	if err != nil {
		return 0, err
	}
	return revision, tx.Commit()
}

func TestDraftServiceGuardsRevisionsValidationAndOwnership(t *testing.T) {
	a, _ := fixture.DraftFixture(t)
	ctx := auth.WithUser(t.Context(), auth.User{ID: 1})
	d, err := flow.Decode(fixture.StructuredDraft)
	if err != nil {
		t.Fatal(err)
	}
	for expected := int64(0); expected < 2; expected++ {
		revision, err := saveDraftCandidate(ctx, a.DB, a.Bots, expected, d)
		if err != nil || revision != expected+1 {
			t.Fatalf("save revision: %d %v", revision, err)
		}
		// Re-saving identical content still advances the revision, avoiding ABA.
		if _, err := saveDraftCandidate(ctx, a.DB, a.Bots, expected, d); !errors.Is(err, bot.ErrStaleDraft) {
			t.Fatalf("stale save: %v", err)
		}
	}
	before, err := a.Bots.LoadDraft(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	invalid := d
	invalid.Welcome.Text = ""
	if _, err := saveDraftCandidate(ctx, a.DB, a.Bots, 2, invalid); err == nil {
		t.Fatal("invalid definition accepted")
	}
	other := auth.WithUser(t.Context(), auth.User{ID: 2})
	if _, err := a.Bots.LoadDraft(other, 1); !errors.Is(err, bot.ErrNotFound) {
		t.Fatalf("cross-owner load: %v", err)
	}
	if _, err := saveDraftCandidate(other, a.DB, a.Bots, 2, d); !errors.Is(err, bot.ErrNotFound) {
		t.Fatalf("cross-owner save: %v", err)
	}
	if _, err := saveDraftCandidate(t.Context(), a.DB, a.Bots, 2, d); err == nil {
		t.Fatal("anonymous save accepted")
	}
	after, err := a.Bots.LoadDraft(ctx, 1)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatal("rejected candidate changed saved Draft")
	}
}

func TestDraftServiceStorageFailurePreservesSnapshot(t *testing.T) {
	a, _ := fixture.DraftFixture(t)
	ctx := auth.WithUser(t.Context(), auth.User{ID: 1})
	d, err := flow.Decode(fixture.StructuredDraft)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := saveDraftCandidate(ctx, a.DB, a.Bots, 0, d); err != nil {
		t.Fatal(err)
	}
	before, err := a.Bots.LoadDraft(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.DB.Exec("CREATE TRIGGER fail_draft BEFORE UPDATE ON bot_drafts BEGIN SELECT RAISE(ABORT, 'forced failure'); END"); err != nil {
		t.Fatal(err)
	}
	d.Welcome.Text = "Failed candidate"
	if _, err := saveDraftCandidate(ctx, a.DB, a.Bots, 1, d); err == nil {
		t.Fatal("storage failure accepted")
	}
	after, err := a.Bots.LoadDraft(ctx, 1)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Fatal("storage failure changed saved Draft")
	}
}

func TestConcurrentDraftTransactionsAcrossAppsCommitOneRevision(t *testing.T) {
	a, _ := fixture.DraftFixture(t)
	second, err := fixture.New(t.Context(), a.Config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.DB.Close() })
	ctx := auth.WithUser(t.Context(), auth.User{ID: 1})
	for expected := int64(0); expected < 2; expected++ {
		start := make(chan struct{})
		type result struct {
			candidate flow.Definition
			revision  int64
			err       error
		}
		results := make(chan result, 2)
		for i, app := range []*fixture.HTTP{a, second} {
			d, err := flow.Decode(fixture.StructuredDraft)
			if err != nil {
				t.Fatal(err)
			}
			d.Welcome.Text = []string{"First candidate", "Second candidate"}[i]
			go func() {
				<-start
				revision, err := saveDraftCandidate(ctx, app.DB, app.Bots, expected, d)
				results <- result{d, revision, err}
			}()
		}
		close(start)
		winner, loser := <-results, <-results
		if winner.err != nil {
			winner, loser = loser, winner
		}
		if winner.err != nil || winner.revision != expected+1 || !errors.Is(loser.err, bot.ErrStaleDraft) {
			t.Fatalf("concurrent saves: %v / %v", winner.err, loser.err)
		}
		current, err := a.Bots.LoadDraft(ctx, 1)
		if err != nil || current.Revision != winner.revision || !reflect.DeepEqual(current.Definition, winner.candidate) {
			t.Fatal("concurrent save lost winning candidate")
		}
	}
}
