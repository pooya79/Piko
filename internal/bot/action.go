package bot

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/pooya79/Piko/internal/platform/database"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
)

var ErrStaleAction = errors.New("action proposal state changed")
var ErrAction = errors.New("unsupported action proposal")

// ActionProposal contains only the owner-visible state that was proposed.
type ActionProposal struct {
	ID, BotID, Revision, DraftRevision, PublishedVersion int64
	Action, BotName, DeliveryState, Receiver, Result     string
	Paused, Stale                                        bool
	Version                                              int64
}

func (p ActionProposal) URL() string {
	return "/bots/" + strconv.FormatInt(p.BotID, 10) + "/proposals/" + strconv.FormatInt(p.ID, 10) + "/confirm"
}
func (p ActionProposal) CanConfirm() bool {
	if p.Stale || p.Result != "pending" {
		return false
	}
	if p.Action == "deploy" {
		return p.DeliveryState != "unconnected" && p.DeliveryState != "disconnected"
	}
	return (p.DeliveryState == "active" || p.DeliveryState == "paused") && ((p.Action == "pause" && !p.Paused) || (p.Action == "resume" && p.Paused))
}

func (s *Service) actionReceiver() string { return string(s.deliveryMode()) + ":" + s.publicURL }

// PrepareAction is read-only. The Builder can stage exactly one bounded action;
// completion persists it, and only the owner's separate HTTP POST can execute.
func (s *Service) PrepareAction(ctx context.Context, botID int64, action string) (ActionProposal, error) {
	if action != "deploy" && action != "pause" && action != "resume" {
		return ActionProposal{}, ErrAction
	}
	ownerID, err := owner(ctx)
	if err != nil {
		return ActionProposal{}, err
	}
	tx, err := s.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return ActionProposal{}, err
	}
	defer func() { _ = tx.Rollback() }()
	q := dbgen.New(tx)
	b, err := q.GetOwnerBot(ctx, dbgen.GetOwnerBotParams{OwnerID: ownerID, ID: botID})
	if err != nil {
		return ActionProposal{}, err
	}
	revision, err := q.GetOwnerActionRevision(ctx, dbgen.GetOwnerActionRevisionParams{OwnerID: ownerID, BotID: botID})
	if err != nil {
		return ActionProposal{}, err
	}
	draft, err := q.GetOwnerDraft(ctx, dbgen.GetOwnerDraftParams{OwnerID: ownerID, BotID: botID})
	if err != nil {
		return ActionProposal{}, err
	}
	state := s.deliveryStatus(botFromRow(b))
	p := ActionProposal{BotID: botID, Action: action, Revision: revision, DraftRevision: draft.Revision, BotName: b.Name, DeliveryState: state.DeliveryState, Paused: state.Paused, PublishedVersion: state.PublishedVersion, Receiver: s.actionReceiver(), Result: "pending"}
	return p, tx.Commit()
}

// SaveActionTx serializes the staged snapshot with successful Builder completion.
func (s *Service) SaveActionTx(ctx context.Context, tx *sql.Tx, chatID, runID int64, p ActionProposal) error {
	ownerID, err := owner(ctx)
	if err != nil {
		return err
	}
	q := dbgen.New(tx)
	revision, err := q.GetOwnerActionRevision(ctx, dbgen.GetOwnerActionRevisionParams{OwnerID: ownerID, BotID: p.BotID})
	if err != nil {
		return err
	}
	if revision != p.Revision || p.Receiver != s.actionReceiver() {
		return ErrStaleAction
	}
	n, err := q.CreateOwnerActionProposal(ctx, dbgen.CreateOwnerActionProposalParams{OwnerID: ownerID, BotID: p.BotID, ChatID: chatID, RunID: runID, Action: p.Action, DraftRevision: p.DraftRevision, DeliveryState: p.DeliveryState, PublishedVersion: p.PublishedVersion, Receiver: p.Receiver})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) ActionProposalsTx(ctx context.Context, tx *sql.Tx, botID, chatID int64) ([]ActionProposal, error) {
	ownerID, err := owner(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := dbgen.New(tx).ListOwnerActionProposals(ctx, dbgen.ListOwnerActionProposalsParams{OwnerID: ownerID, BotID: botID, ChatID: chatID})
	if err != nil {
		return nil, err
	}
	out := make([]ActionProposal, 0, len(rows))
	for _, row := range rows {
		out = append(out, s.actionFromRow(dbgen.GetOwnerActionProposalRow(row)))
	}
	return out, nil
}

func (s *Service) actionFromRow(r dbgen.GetOwnerActionProposalRow) ActionProposal {
	return ActionProposal{ID: r.ID, BotID: r.BotID, Action: r.Action, Revision: r.ActionRevision, DraftRevision: r.DraftRevision, BotName: r.BotName, DeliveryState: r.DeliveryState, Paused: r.Paused != 0, PublishedVersion: r.PublishedVersion, Receiver: r.Receiver, Result: r.Result, Version: r.Version, Stale: r.ActionRevision != r.CurrentRevision || r.Receiver != s.actionReceiver()}
}

type ActionOutcome struct {
	Proposal   ActionProposal
	Deployment Deployment
	Key        string
}

func (s *Service) ConfirmAction(ctx context.Context, botID, proposalID int64, operate bool) (ActionOutcome, error) {
	ownerID, err := owner(ctx)
	if err != nil {
		return ActionOutcome{}, err
	}
	var out ActionOutcome
	execute := false
	err = database.RetryWrite(ctx, s.repo.db, func(conn *sql.Conn) error {
		execute = false
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		q := dbgen.New(tx)
		row, err := q.GetOwnerActionProposal(ctx, dbgen.GetOwnerActionProposalParams{OwnerID: ownerID, BotID: botID, ProposalID: proposalID})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		out.Proposal = s.actionFromRow(row)
		out.Key = row.Result
		out.Deployment.Version = row.Version
		// A lost response never repeats publication or activation, even after restart.
		if row.Result != "pending" {
			return nil
		}
		if out.Proposal.Stale {
			return ErrStaleAction
		}
		active, err := q.ActiveOwnerBuilderBot(ctx, dbgen.ActiveOwnerBuilderBotParams{OwnerID: ownerID, BotID: sql.NullInt64{Int64: botID, Valid: true}})
		if err != nil {
			return err
		}
		if active > 0 {
			return ErrBuilderBusy
		}
		if row.Action == "deploy" {
			if !operate {
				return ErrAction
			}
			out.Deployment.Version, err = s.publishTx(ctx, q, ownerID, botID, true)
			if err != nil {
				return err
			}
			out.Key = "action.executing"
			execute = true
		} else {
			if !out.Proposal.CanConfirm() {
				return ErrPauseUnavailable
			}
			if err := s.setPaused(ctx, q, ownerID, botID, row.Action == "pause"); err != nil {
				return err
			}
			out.Key = "action." + row.Action + ".success"
		}
		if err := q.SaveOwnerActionOutcome(ctx, dbgen.SaveOwnerActionOutcomeParams{OwnerID: ownerID, BotID: botID, ProposalID: proposalID, Result: out.Key, Version: out.Deployment.Version}); err != nil {
			return err
		}
		return tx.Commit()
	})
	if err != nil {
		return out, err
	}
	if execute {
		out.Deployment, err = s.activateDeployment(ctx, botID, out.Deployment.Version, "")
		out.Key = "deploy.success"
		if err != nil {
			out.Key = "deploy.partial"
		}
		// Persist partial/uncertain outcomes independently of the browser connection.
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deliverySaveTimeout)
		defer cancel()
		saveErr := s.repo.q.SaveOwnerActionOutcome(saveCtx, dbgen.SaveOwnerActionOutcomeParams{OwnerID: ownerID, BotID: botID, ProposalID: proposalID, Result: out.Key, Version: out.Deployment.Version})
		if saveErr != nil {
			return out, saveErr
		}
	}
	out.Deployment.Bot, err = s.Get(ctx, botID)
	return out, err
}
