package httpfixture

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot"
	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/platform/database"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
)

// Draft setup uses the transactional service used by Builder, independently of
// HTTP editing routes. Resolve the owner from the browser's real session.
func (b *Browser) draftStore(t *testing.T) (*sql.DB, *bot.Service, context.Context, error) {
	t.Helper()
	if b.DatabasePath == "" {
		return nil, nil, nil, fmt.Errorf("missing Draft fixture database path")
	}
	db, err := database.Open(t.Context(), b.DatabasePath)
	if err != nil {
		return nil, nil, nil, err
	}
	session, err := auth.NewService(dbgen.New(db)).LoadSession(t.Context(), b.Cookie(auth.SessionCookie))
	if err != nil {
		_ = db.Close()
		return nil, nil, nil, err
	}
	return db, TestBotService(t, db), auth.WithUser(t.Context(), session.User), nil
}

func (b *Browser) SaveDraft(t *testing.T, botID int64, values url.Values) error {
	t.Helper()
	db, service, ctx, err := b.draftStore(t)
	if err != nil {
		return err
	}
	defer db.Close()
	d, err := DraftDefinition(values)
	if err != nil {
		return err
	}
	snapshot, err := service.LoadDraft(ctx, botID)
	if err != nil {
		return err
	}
	revision := snapshot.Revision
	if raw := values.Get("draft_revision"); raw != "" {
		revision, err = strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return err
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := service.SaveDraftTx(ctx, tx, botID, revision, d); err != nil {
		return err
	}
	return tx.Commit()
}

// Values keep existing runtime fixtures readable while the service receives a
// Flow definition. This helper only converts test data; it is not an HTTP parser.
func DraftDefinition(v url.Values) (flow.Definition, error) {
	if raw := v.Get("definition"); raw != "" {
		return flow.Decode(raw)
	}
	d := flow.Definition{Version: 1, Welcome: flow.Block{ID: "welcome", Type: "message", Text: v.Get("welcome")}, Menu: flow.Block{ID: "menu", Type: "menu", Text: v.Get("menu_prompt")}}
	if id := v.Get("welcome_id"); id != "" {
		d.Welcome.ID = id
	}
	if id := v.Get("menu_id"); id != "" {
		d.Menu.ID = id
	}
	for i, label := range v["choice_label"] {
		id, target := strconv.Itoa(i+1), "reply-"+strconv.Itoa(i+1)
		if len(v["choice_id"]) > i {
			id = v["choice_id"][i]
		}
		if len(v["choice_target"]) > i {
			target = v["choice_target"][i]
		}
		d.Menu.Choices = append(d.Menu.Choices, flow.Choice{ID: id, Label: label, Target: target})
		d.Messages = append(d.Messages, flow.Block{ID: target, Type: "message", Text: v["choice_message"][i]})
	}
	for i, id := range v["form_id"] {
		d.Version = 2
		d.Menu.Choices = append(d.Menu.Choices, flow.Choice{ID: v["form_choice_id"][i], Label: v["form_label"][i], Target: id})
		f := flow.Form{ID: id, Review: v["review_message"][i], Acknowledgement: v["acknowledgement"][i]}
		for j, formID := range v["question_form"] {
			if formID != id {
				continue
			}
			q := flow.Question{ID: v["question_id"][j], Type: v["question_type"][j], Label: v["question_label"][j], Prompt: v["question_prompt"][j], Required: v["question_required"][j] == "yes"}
			if raw := v["question_options"][j]; raw != "" {
				q.Options = strings.Split(raw, "\n")
			}
			if min, max := v["number_min"][j], v["number_max"][j]; min != "" || max != "" {
				q.Number = &flow.NumberRules{Min: min, Max: max}
			}
			if len(v["text_max"]) > j && v["text_max"][j] != "" {
				limit, err := strconv.Atoi(v["text_max"][j])
				if err != nil {
					return d, err
				}
				q.MaxLength = limit
			}
			if len(v["date_min"]) > j && len(v["date_max"]) > j {
				if min, max := v["date_min"][j], v["date_max"][j]; min != "" || max != "" {
					q.Date = &flow.DateRules{Min: min, Max: max}
				}
			}
			f.Questions = append(f.Questions, q)
		}
		d.Forms = append(d.Forms, f)
	}
	if order := v["menu_order"]; len(order) > 0 {
		choices := make([]flow.Choice, 0, len(order))
		for _, id := range order {
			for _, c := range d.Menu.Choices {
				if c.ID == id {
					choices = append(choices, c)
				}
			}
		}
		d.Menu.Choices = choices
	}
	return d, nil
}

func (b *Browser) LoadDraft(t *testing.T, botID int64) url.Values {
	t.Helper()
	db, service, ctx, err := b.draftStore(t)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	snapshot, err := service.LoadDraft(ctx, botID)
	if err != nil {
		t.Fatal(err)
	}
	d := snapshot.Definition
	v := url.Values{"welcome": {d.Welcome.Text}, "menu_prompt": {d.Menu.Text}, "welcome_id": {d.Welcome.ID}, "menu_id": {d.Menu.ID}, "draft_revision": {strconv.FormatInt(snapshot.Revision, 10)}}
	for _, c := range d.Menu.Choices {
		v.Add("menu_order", c.ID)
		if text, ok := d.Message(c.Target); ok {
			v.Add("choice_id", c.ID)
			v.Add("choice_target", c.Target)
			v.Add("choice_label", c.Label)
			v.Add("choice_message", text)
		} else if f, ok := d.Form(c.Target); ok {
			v.Add("form_id", f.ID)
			v.Add("form_choice_id", c.ID)
			v.Add("form_label", c.Label)
			v.Add("review_message", f.Review)
			v.Add("acknowledgement", f.Acknowledgement)
			for _, q := range f.Questions {
				v.Add("question_form", f.ID)
				v.Add("question_id", q.ID)
				v.Add("question_type", q.Type)
				v.Add("question_label", q.Label)
				v.Add("question_prompt", q.Prompt)
				required := "no"
				if q.Required {
					required = "yes"
				}
				v.Add("question_required", required)
				v.Add("question_options", strings.Join(q.Options, "\n"))
				min, max := "", ""
				if q.Number != nil {
					min, max = q.Number.Min, q.Number.Max
				}
				v.Add("number_min", min)
				v.Add("number_max", max)
				min, max = "", ""
				if q.Date != nil {
					min, max = q.Date.Min, q.Date.Max
				}
				v.Add("date_min", min)
				v.Add("date_max", max)
				limit := ""
				if q.MaxLength != 0 {
					limit = strconv.Itoa(q.MaxLength)
				}
				v.Add("text_max", limit)
			}
		}
	}
	return v
}

func (b *Browser) DraftText(t *testing.T, botID int64) string {
	t.Helper()
	return fmt.Sprint(b.LoadDraft(t, botID))
}
