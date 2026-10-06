package bot

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/locale"
	"github.com/pooya79/Piko/internal/web"
	"github.com/pooya79/Piko/internal/web/request"
	"github.com/pooya79/Piko/internal/web/shell"
)

func flowPage(ctx context.Context, b Bot, titleKey string) shell.Page {
	var section shell.BotNavSection
	switch titleKey {
	case "flow.title":
		section = shell.BotFlow
	case "draft.title":
		section = shell.BotDraft
	case "submission.title", "submission.detail":
		section = shell.BotSubmissions
	case "bot.connection.title", "bot.connect.title":
		section = shell.BotConnection
	case "bot.settings.title":
		section = shell.BotSettings
	}
	return shell.Page{Title: locale.T(ctx, titleKey), BotURL: b.URL(), BotSection: section, ActiveNav: shell.BotsNav, Breadcrumbs: []shell.Breadcrumb{{Label: locale.T(ctx, "workspace.bots"), URL: "/bots"}, {Label: b.Name, URL: b.URL()}, {Label: locale.T(ctx, titleKey)}}}
}

func (h *Handler) requestedBot(w http.ResponseWriter, r *http.Request) (Bot, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "botID"), 10, 64)
	if err != nil || id <= 0 {
		web.RenderError(w, r, 404, "error.message.page.missing")
		return Bot{}, false
	}
	b, err := h.service.Get(r.Context(), id)
	if err != nil {
		h.draftError(w, r, err)
		return Bot{}, false
	}
	return b, true
}

func (h *Handler) Draft(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	snapshot, err := h.service.LoadDraft(r.Context(), b.ID)
	if err != nil {
		h.draftError(w, r, err)
		return
	}
	d, revision := snapshot.Definition, snapshot.Revision
	saved := revision > 0
	if !saved {
		d = emptyDraft()
	}
	if name := r.URL.Query().Get("template"); name != "" && name != "welcome" {
		if _, ok := templateDefinition(name); ok {
			saved = false
			if err := addTemplate(&d, name); err != nil {
				h.draftPage(w, r, 422, b, d, revision, false, locale.T(r.Context(), "draft.error.choices"))
				return
			}
		}
	}
	h.draftPage(w, r, 200, b, d, revision, saved, "")
}

func (h *Handler) SaveDraft(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.draftPage(w, r, 422, b, emptyDraft(), 0, false, locale.T(r.Context(), "draft.error.definition"))
		return
	}
	revision, revisionErr := strconv.ParseInt(r.PostForm.Get("draft_revision"), 10, 64)
	if len(r.PostForm["draft_revision"]) != 1 || revisionErr != nil || revision < 0 {
		h.draftPage(w, r, 422, b, emptyDraft(), 0, false, locale.T(r.Context(), "draft.error.revision"))
		return
	}
	d, err := parseDraft(r.PostForm)
	if values, supplied := r.PostForm["definition"]; supplied {
		if len(values) != 1 {
			err = &flow.Invalid{Key: "draft.error.definition"}
		} else {
			d, err = flow.Decode(values[0])
		}
	}
	if err == nil && r.PostForm.Get("edit") != "" {
		action := r.PostForm.Get("edit")
		parts := strings.Split(action, ":")
		questionType := ""
		if len(parts) >= 3 {
			questionType = r.PostForm.Get("add_type_" + parts[2])
		}
		if len(r.PostForm["edit"]) != 1 {
			err = &flow.Invalid{Key: "draft.error.definition"}
		} else {
			editor, editErr := editDraft(&d, action, questionType)
			err = editErr
			if err == nil {
				h.draftPage(w, r, 200, b, d, revision, false, "", editor)
				return
			}
		}
	}
	if err == nil {
		_, err = h.service.SaveDraft(r.Context(), b.ID, revision, d)
	}
	if err != nil {
		if errors.Is(err, ErrStaleDraft) {
			h.draftPage(w, r, 409, b, d, revision, false, locale.T(r.Context(), "draft.error.conflict"))
			return
		}
		var invalid *flow.Invalid
		if errors.As(err, &invalid) {
			h.draftPage(w, r, 422, b, d, revision, false, locale.T(r.Context(), invalid.Key))
			return
		}
		h.draftError(w, r, err)
		return
	}
	http.Redirect(w, r, b.URL()+"/draft?saved=1", http.StatusSeeOther)
}

// Parallel HTTP fields have an explicit Form identity for each Question.
// Missing, duplicated or mismatched fields cannot silently shift answers/routes.
func parseDraft(v url.Values) (flow.Definition, error) {
	d := emptyDraft()
	d.Welcome.Text = v.Get("welcome")
	d.Menu.Text = v.Get("menu_prompt")
	bad := &flow.Invalid{Key: "draft.error.definition"}
	for _, key := range []string{"welcome", "menu_prompt", "welcome_id", "menu_id"} {
		if len(v[key]) > 1 {
			return d, bad
		}
	}
	if id := v.Get("welcome_id"); id != "" {
		d.Welcome.ID = id
	}
	if id := v.Get("menu_id"); id != "" {
		d.Menu.ID = id
	}
	labels, messages := v["choice_label"], v["choice_message"]
	if len(labels) != len(messages) || len(labels) > flow.MaxChoices {
		return d, &flow.Invalid{Key: "draft.error.choices"}
	}
	for _, key := range []string{"choice_id", "choice_target"} {
		if len(v[key]) != 0 && len(v[key]) != len(labels) {
			return d, bad
		}
	}
	for i, label := range labels {
		if strings.TrimSpace(label) == "" && strings.TrimSpace(messages[i]) == "" {
			continue
		}
		id, target := strconv.Itoa(i+1), "reply-"+strconv.Itoa(i+1)
		if len(v["choice_id"]) != 0 {
			id = v["choice_id"][i]
		}
		if len(v["choice_target"]) != 0 {
			target = v["choice_target"][i]
		}
		d.Menu.Choices = append(d.Menu.Choices, flow.Choice{ID: id, Label: label, Target: target})
		d.Messages = append(d.Messages, flow.Block{ID: target, Type: "message", Text: messages[i]})
	}
	ids := v["form_id"]
	if len(ids)+len(d.Messages) > flow.MaxChoices {
		return d, &flow.Invalid{Key: "draft.error.choices"}
	}
	for _, key := range []string{"form_choice_id", "form_label", "review_message", "acknowledgement"} {
		if len(v[key]) != len(ids) {
			return d, bad
		}
	}
	for i, id := range ids {
		if formIndex(d, id) >= 0 {
			return d, bad
		}
		d.Forms = append(d.Forms, flow.Form{ID: id, Review: v["review_message"][i], Acknowledgement: v["acknowledgement"][i]})
		d.Menu.Choices = append(d.Menu.Choices, flow.Choice{ID: v["form_choice_id"][i], Label: v["form_label"][i], Target: id})
	}
	n := len(v["question_id"])
	if n > flow.MaxChoices*flow.MaxQuestions {
		return d, bad
	}
	for _, key := range []string{"question_form", "question_type", "question_label", "question_prompt", "question_required", "question_options", "number_min", "number_max", "text_max", "date_min", "date_max"} {
		if len(v[key]) != n {
			return d, bad
		}
	}
	var parseErr error
	for i, id := range v["question_id"] {
		fi := formIndex(d, v["question_form"][i])
		if fi < 0 || len(d.Forms[fi].Questions) >= flow.MaxQuestions {
			return d, bad
		}
		required := v["question_required"][i]
		if required != "yes" && required != "no" {
			return d, bad
		}
		q := flow.Question{ID: id, Label: v["question_label"][i], Prompt: v["question_prompt"][i], Type: v["question_type"][i], Required: required == "yes"}
		if raw := v["question_options"][i]; raw != "" {
			q.Options = strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
			for j := range q.Options {
				q.Options[j] = strings.TrimSpace(q.Options[j])
			}
		}
		min, max := strings.TrimSpace(v["number_min"][i]), strings.TrimSpace(v["number_max"][i])
		if min != "" || max != "" {
			q.Number = &flow.NumberRules{Min: min, Max: max}
		}
		if raw := v["text_max"][i]; raw != "" {
			limit, err := strconv.Atoi(raw)
			if err != nil || limit <= 0 {
				parseErr = bad
			}
			q.MaxLength = limit
		}
		min, max = strings.TrimSpace(v["date_min"][i]), strings.TrimSpace(v["date_max"][i])
		if min != "" || max != "" {
			q.Date = &flow.DateRules{Min: min, Max: max}
		}
		d.Forms[fi].Questions = append(d.Forms[fi].Questions, q)
	}
	if len(d.Forms) > 0 {
		d.Version = 2
	}
	if order, supplied := v["menu_order"]; supplied {
		if len(order) != len(d.Menu.Choices) {
			return d, bad
		}
		sorted := make([]flow.Choice, 0, len(order))
		seen := map[string]bool{}
		for _, id := range order {
			if seen[id] {
				return d, bad
			}
			seen[id] = true
			found := false
			for _, c := range d.Menu.Choices {
				if c.ID == id {
					sorted = append(sorted, c)
					found = true
					break
				}
			}
			if !found {
				return d, bad
			}
		}
		d.Menu.Choices = sorted
	}
	return d, parseErr
}

func (h *Handler) draftPage(w http.ResponseWriter, r *http.Request, status int, b Bot, d flow.Definition, revision int64, saved bool, message string, editors ...draftEditor) {
	// HTML submits Forms in menu order, including action indexes.
	forms := make([]flow.Form, 0, len(d.Forms))
	for _, c := range d.Menu.Choices {
		if f, ok := d.Form(c.Target); ok {
			forms = append(forms, f)
		}
	}
	d.Forms = forms
	u, _ := auth.UserFromContext(r.Context())
	editor := draftEditor{}
	if len(editors) > 0 {
		editor = editors[0]
	}
	// Keep an invalid length exactly as entered while retaining the full Flow.
	v := r.PostForm
	if len(v["text_max"]) == len(v["question_id"]) && len(v["question_form"]) == len(v["question_id"]) {
		editor.TextLimits = make(map[questionRef]string, len(v["question_id"]))
		for i, id := range v["question_id"] {
			raw := v["text_max"][i]
			editor.TextLimits[questionRef{v["question_form"][i], id}] = raw
			if message != "" && editor.Target == "" && raw != "" {
				limit, err := strconv.Atoi(raw)
				if err != nil || limit <= 0 {
					editor.Target, editor.Question = v["question_form"][i], id
				}
			}
		}
	}
	h.render(w, r, status, DraftPage(u.DisplayName, request.CookieValue(r, auth.CSRFCookie), b, d, revision, saved, r.URL.Query().Get("saved") == "1", message, editor))
}

func (h *Handler) draftError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrNotFound) {
		web.RenderError(w, r, 404, "error.message.page.missing")
		return
	}
	h.failed(w, r)
}
