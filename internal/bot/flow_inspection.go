package bot

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
)

// FlowInspection is an owner-authorized saved snapshot; revision zero is empty.
type FlowInspection struct {
	Canvas    flow.Canvas
	Revision  int64
	Published bool
	PikoURL   string
}

func (s *Service) InspectFlow(ctx context.Context, botID int64, published bool) (FlowInspection, error) {
	b, err := s.Get(ctx, botID)
	if err != nil {
		return FlowInspection{}, err
	}
	ownerID, err := owner(ctx)
	if err != nil {
		return FlowInspection{}, err
	}
	view := FlowInspection{Published: published, PikoURL: b.URL() + "/studio"}
	var snapshot Draft
	if published {
		snapshot, err = s.repo.loadPublishedFlow(ctx, ownerID, botID)
	} else {
		snapshot, err = s.repo.loadDraft(ctx, ownerID, botID)
	}
	if err != nil {
		return FlowInspection{}, err
	}
	view.Revision = snapshot.Revision
	if snapshot.Revision > 0 {
		view.Canvas = flow.ProjectCanvas(snapshot.Definition)
	}
	chatID, err := s.repo.q.LatestOwnerBotChat(ctx, dbgen.LatestOwnerBotChatParams{OwnerID: ownerID, BotID: botID})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return FlowInspection{}, err
	}
	if err == nil {
		view.PikoURL = b.URL() + "/chats/" + strconv.FormatInt(chatID, 10)
	}
	return view, nil
}

func (r *Repository) loadPublishedFlow(ctx context.Context, ownerID, botID int64) (Draft, error) {
	row, err := r.q.GetOwnerLatestPublication(ctx, dbgen.GetOwnerLatestPublicationParams{OwnerID: ownerID, BotID: botID})
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, nil
	}
	if err != nil {
		return Draft{}, err
	}
	d, err := flow.Decode(row.Definition)
	return Draft{Definition: d, Revision: row.Version}, err
}
