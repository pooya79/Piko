package bot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/bot/templates/booking"
	"github.com/pooya79/Piko/internal/bot/templates/inquiry"
	"github.com/pooya79/Piko/internal/bot/templates/registration"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
)

// Draft edits preserve identities while labels, ordering and questions change.
func emptyDraft() flow.Definition {
	return flow.Definition{Version: 1, Welcome: flow.Block{ID: "welcome", Type: "message", Text: "سلام! از منو شروع کنید."}, Menu: flow.Block{ID: "menu", Type: "menu", Text: "چه کاری می\u200cخواهید انجام دهید؟"}}
}

func templateDefinition(name string) (flow.Definition, bool) {
	switch name {
	case "inquiry":
		return inquiry.Default().Definition(), true
	case "registration":
		return registration.Default().Definition(), true
	case "booking":
		return booking.Default().Definition(), true
	default:
		return flow.Definition{}, false
	}
}

func uniqueID(base string, used func(string) bool) string {
	for n := 1; ; n++ {
		id := base
		if n > 1 {
			id += "-" + strconv.Itoa(n)
		}
		if !used(id) {
			return id
		}
	}
}

func addTemplate(d *flow.Definition, name string) error {
	if len(d.Menu.Choices) >= flow.MaxChoices {
		return &flow.Invalid{Key: "draft.error.choices"}
	}
	if name == "message" {
		id := uniqueID("reply", func(id string) bool { return blockIDUsed(*d, id) })
		choice := uniqueID("message", func(id string) bool { return choiceIDUsed(*d, id) })
		d.Messages = append(d.Messages, flow.Block{ID: id, Type: "message", Text: "پیام خود را بنویسید."})
		d.Menu.Choices = append(d.Menu.Choices, flow.Choice{ID: choice, Label: "پیام " + strconv.Itoa(len(d.Menu.Choices)+1), Target: id})
		return nil
	}
	t, ok := templateDefinition(name)
	if !ok {
		return &flow.Invalid{Key: "draft.error.definition"}
	}
	f := t.Forms[0]
	f.ID = uniqueID(f.ID, func(id string) bool { return blockIDUsed(*d, id) })
	c := t.Menu.Choices[0]
	c.ID = uniqueID(c.ID, func(id string) bool { return choiceIDUsed(*d, id) })
	c.Target = f.ID
	for _, existing := range d.Menu.Choices {
		if existing.Label == c.Label {
			c.Label += " " + strconv.Itoa(len(d.Menu.Choices)+1)
			break
		}
	}
	d.Version = 2
	d.Forms = append(d.Forms, f)
	d.Menu.Choices = append(d.Menu.Choices, c)
	return nil
}

func blockIDUsed(d flow.Definition, id string) bool {
	if d.Welcome.ID == id || d.Menu.ID == id {
		return true
	}
	if _, ok := d.Form(id); ok {
		return true
	}
	_, ok := d.Message(id)
	return ok
}
func choiceIDUsed(d flow.Definition, id string) bool {
	for _, c := range d.Menu.Choices {
		if c.ID == id {
			return true
		}
	}
	return false
}

// Editing changes only the rendered settings. Save is the persistence boundary.
func editDraft(d *flow.Definition, action, questionType string) (draftEditor, error) {
	parts := strings.Split(action, ":")
	bad := &flow.Invalid{Key: "draft.error.definition"}
	if len(parts) == 2 && parts[0] == "add" {
		if err := addTemplate(d, parts[1]); err != nil {
			return draftEditor{}, err
		}
		return draftEditor{Target: d.Menu.Choices[len(d.Menu.Choices)-1].Target}, nil
	}
	if len(parts) < 3 {
		return draftEditor{}, bad
	}
	index, err := strconv.Atoi(parts[2])
	if err != nil || index < 0 {
		return draftEditor{}, bad
	}
	if parts[0] == "menu" && len(parts) == 3 {
		if index >= len(d.Menu.Choices) {
			return draftEditor{}, bad
		}
		switch parts[1] {
		case "up":
			if index == 0 {
				return draftEditor{}, bad
			}
			d.Menu.Choices[index-1], d.Menu.Choices[index] = d.Menu.Choices[index], d.Menu.Choices[index-1]
			index--
		case "down":
			if index+1 >= len(d.Menu.Choices) {
				return draftEditor{}, bad
			}
			d.Menu.Choices[index+1], d.Menu.Choices[index] = d.Menu.Choices[index], d.Menu.Choices[index+1]
			index++
		case "remove":
			target := d.Menu.Choices[index].Target
			for i, f := range d.Forms {
				if f.ID == target {
					d.Forms = append(d.Forms[:i], d.Forms[i+1:]...)
					break
				}
			}
			for i, m := range d.Messages {
				if m.ID == target {
					d.Messages = append(d.Messages[:i], d.Messages[i+1:]...)
					break
				}
			}
			d.Menu.Choices = append(d.Menu.Choices[:index], d.Menu.Choices[index+1:]...)
			if index == len(d.Menu.Choices) {
				index--
			}
		default:
			return draftEditor{}, bad
		}
		if index >= 0 {
			return draftEditor{Target: d.Menu.Choices[index].Target}, nil
		}
		return draftEditor{}, nil
	}
	if parts[0] != "question" || index >= len(d.Forms) {
		return draftEditor{}, bad
	}
	f := &d.Forms[index]
	if len(parts) == 3 && parts[1] == "add" {
		if len(f.Questions) >= flow.MaxQuestions {
			return draftEditor{}, bad
		}
		q := flow.Question{ID: uniqueID("question", func(id string) bool {
			for _, q := range f.Questions {
				if q.ID == id {
					return true
				}
			}
			return false
		}), Label: "پرسش تازه", Prompt: "پرسش خود را بنویسید.", Type: questionType, Required: true}
		switch questionType {
		case "short_text", "long_text", "phone", "date", "number":
		case "single_choice":
			q.Options = []string{"گزینه اول", "گزینه دوم"}
		default:
			return draftEditor{}, bad
		}
		f.Questions = append(f.Questions, q)
		return draftEditor{Target: f.ID, Question: q.ID}, nil
	}
	if len(parts) != 4 {
		return draftEditor{}, bad
	}
	qi, err := strconv.Atoi(parts[3])
	if err != nil || qi < 0 || qi >= len(f.Questions) {
		return draftEditor{}, bad
	}
	switch parts[1] {
	case "up":
		if qi == 0 {
			return draftEditor{}, bad
		}
		f.Questions[qi-1], f.Questions[qi] = f.Questions[qi], f.Questions[qi-1]
		qi--
	case "down":
		if qi+1 >= len(f.Questions) {
			return draftEditor{}, bad
		}
		f.Questions[qi+1], f.Questions[qi] = f.Questions[qi], f.Questions[qi+1]
		qi++
	case "remove":
		f.Questions = append(f.Questions[:qi], f.Questions[qi+1:]...)
		if qi == len(f.Questions) {
			qi--
		}
	default:
		return draftEditor{}, bad
	}
	focus := draftEditor{Target: f.ID}
	if qi >= 0 {
		focus.Question = f.Questions[qi].ID
	}
	return focus, nil
}

func formIndex(d flow.Definition, id string) int {
	for i, f := range d.Forms {
		if f.ID == id {
			return i
		}
	}
	return -1
}

// Draft is a validated saved snapshot. Revision zero means no Draft has been saved.
type Draft struct {
	Definition flow.Definition
	Revision   int64
}

var ErrStaleDraft = errors.New("Draft has changed since it was loaded")

func (s *Service) LoadDraft(ctx context.Context, botID int64) (Draft, error) {
	if _, err := s.Get(ctx, botID); err != nil {
		return Draft{}, err
	}
	ownerID, err := owner(ctx)
	if err != nil {
		return Draft{}, err
	}
	return s.repo.loadDraft(ctx, ownerID, botID)
}

// SaveDraft applies one candidate atomically against the revision its caller read.
// Manual edits, future Builder results and Undo must all use this boundary.
func (s *Service) SaveDraft(ctx context.Context, botID, expectedRevision int64, d flow.Definition) (int64, error) {
	ownerID, err := owner(ctx)
	if err != nil {
		return 0, err
	}
	tx, err := s.repo.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.repo.q.WithTx(tx)
	// The configured immediate transaction serializes ownership and first saves.
	if _, err := q.GetOwnerBot(ctx, dbgen.GetOwnerBotParams{OwnerID: ownerID, ID: botID}); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	if err := d.Validate(); err != nil {
		return 0, err
	}
	data, err := json.Marshal(d)
	if err != nil {
		return 0, err
	}
	revision, err := q.SaveOwnerDraft(ctx, dbgen.SaveOwnerDraftParams{OwnerID: ownerID, BotID: botID, Definition: string(data), ExpectedRevision: expectedRevision})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrStaleDraft
	}
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return revision, nil
}

func numberBound(q flow.Question, minimum bool) string {
	if q.Number == nil {
		return ""
	}
	if minimum {
		return q.Number.Min
	}
	return q.Number.Max
}

func dateBound(q flow.Question, minimum bool) string {
	if q.Date == nil {
		return ""
	}
	if minimum {
		return q.Date.Min
	}
	return q.Date.Max
}

// Editor state retains raw validator input on errors and follows edited IDs.
type questionRef struct{ Form, Question string }
type draftEditor struct {
	Target, Question string
	TextLimits       map[questionRef]string
}

func (e draftEditor) textBound(formID string, q flow.Question) string {
	if raw, ok := e.TextLimits[questionRef{formID, q.ID}]; ok {
		return raw
	}
	if q.MaxLength == 0 {
		return ""
	}
	return strconv.Itoa(q.MaxLength)
}

func questionTypes() []string {
	return []string{"short_text", "long_text", "phone", "single_choice", "date", "number"}
}
func questionTypeKey(value string) string {
	return "inquiry.type." + strings.ReplaceAll(value, "_", ".")
}

func templateNames() []string { return []string{"inquiry", "registration", "booking"} }
