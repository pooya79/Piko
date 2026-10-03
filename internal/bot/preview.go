package bot

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"

	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/bot/preview"
)

var (
	ErrNoDraft      = errors.New("save a Draft before Preview")
	ErrStalePreview = errors.New("Preview has advanced")
)

type Preview struct {
	ID           string
	Revision     int64
	Conversation preview.Conversation
	definition   flow.Definition
}

func (s *Service) StartPreview(ctx context.Context, botID int64) (Preview, error) {
	d, saved, err := s.LoadDraft(ctx, botID)
	if err != nil {
		return Preview{}, err
	}
	if !saved {
		return Preview{}, ErrNoDraft
	}
	state, err := preview.Start(d)
	if err != nil {
		return Preview{}, err
	}
	nonce := make([]byte, 18)
	if _, err = rand.Read(nonce); err != nil {
		return Preview{}, err
	}
	p := Preview{ID: base64.RawURLEncoding.EncodeToString(nonce), Revision: 1, Conversation: state, definition: d}
	ownerID, err := owner(ctx)
	if err != nil {
		return Preview{}, err
	}
	if err := s.repo.createPreview(ctx, ownerID, botID, p); err != nil {
		return Preview{}, err
	}
	return p, nil
}

func (s *Service) GetPreview(ctx context.Context, botID int64, id string) (Preview, error) {
	ownerID, err := owner(ctx)
	if err != nil {
		return Preview{}, err
	}
	return s.repo.getPreview(ctx, ownerID, botID, id)
}

func (s *Service) AdvancePreview(ctx context.Context, botID int64, id string, revision int64, choice string, restart bool, answer *string) error {
	p, err := s.GetPreview(ctx, botID, id)
	if err != nil {
		return err
	}
	if revision != p.Revision {
		return ErrStalePreview
	}
	if restart {
		p.Conversation, err = preview.Start(p.definition)
	} else if answer != nil {
		p.Conversation, err = preview.Answer(p.definition, p.Conversation, *answer)
	} else {
		p.Conversation, err = preview.Choose(p.definition, p.Conversation, choice)
	}
	if err != nil {
		return err
	}
	ownerID, err := owner(ctx)
	if err != nil {
		return err
	}
	return s.repo.updatePreview(ctx, ownerID, botID, p)
}
