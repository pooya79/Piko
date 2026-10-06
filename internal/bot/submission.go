package bot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	engine "github.com/pooya79/Piko/internal/bot/runtime"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
	"math"
	"time"
)

type Submission struct {
	ID, ParticipantID, Version int64
	CreatedAt                  time.Time
	Answers                    []engine.Answer
}

func (s Submission) Summary() string {
	for _, a := range s.Answers {
		if a.Value == "" {
			continue
		}
		value := []rune(a.DisplayValue())
		if len(value) > 200 {
			return a.Label + ": " + string(value[:200]) + "…"
		}
		return a.Label + ": " + a.DisplayValue()
	}
	return ""
}

func (s *Service) ListSubmissions(ctx context.Context, botID, before int64) ([]Submission, error) {
	if _, err := s.Get(ctx, botID); err != nil {
		return nil, err
	}
	ownerID, err := owner(ctx)
	if err != nil {
		return nil, err
	}
	if before <= 0 {
		before = math.MaxInt64
	}
	rows, err := s.repo.q.ListOwnerSubmissions(ctx, dbgen.ListOwnerSubmissionsParams{OwnerID: ownerID, BotID: botID, BeforeID: before})
	if err != nil {
		return nil, err
	}
	results := make([]Submission, 0, len(rows))
	for _, r := range rows {
		item := Submission{ID: r.ID, ParticipantID: r.ParticipantID, Version: r.Version, CreatedAt: time.Unix(r.CreatedAt, 0)}
		if err := json.Unmarshal([]byte(r.Answers), &item.Answers); err != nil {
			return nil, err
		}
		results = append(results, item)
	}
	return results, nil
}

func (s *Service) GetSubmission(ctx context.Context, botID, id int64) (Submission, error) {
	ownerID, err := owner(ctx)
	if err != nil {
		return Submission{}, err
	}
	r, err := s.repo.q.GetOwnerSubmission(ctx, dbgen.GetOwnerSubmissionParams{OwnerID: ownerID, BotID: botID, ID: id})
	if errors.Is(err, sql.ErrNoRows) {
		return Submission{}, ErrNotFound
	}
	if err != nil {
		return Submission{}, err
	}
	result := Submission{ID: r.ID, ParticipantID: r.ParticipantID, Version: r.Version, CreatedAt: time.Unix(r.CreatedAt, 0)}
	err = json.Unmarshal([]byte(r.Answers), &result.Answers)
	return result, err
}
