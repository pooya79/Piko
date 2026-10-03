package bot

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/bot/templates/booking"
	"github.com/pooya79/Piko/internal/bot/templates/inquiry"
	"github.com/pooya79/Piko/internal/bot/templates/registration"
	"github.com/pooya79/Piko/internal/locale"
	"github.com/pooya79/Piko/internal/web"
	"github.com/pooya79/Piko/internal/web/request"
	"github.com/pooya79/Piko/internal/web/shell"
)

func flowPage(ctx context.Context, b Bot, titleKey string) shell.Page {
	return shell.Page{Title: locale.T(ctx, titleKey), ActiveNav: shell.BotsNav, Breadcrumbs: []shell.Breadcrumb{{Label: locale.T(ctx, "workspace.bots"), URL: "/bots"}, {Label: b.Name, URL: b.URL()}, {Label: locale.T(ctx, titleKey)}}}
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
	d, saved, err := h.service.LoadDraft(r.Context(), b.ID)
	if err != nil {
		h.draftError(w, r, err)
		return
	}
	s := settings(d)
	if templateFlow, ok := templateDefinition(r.URL.Query().Get("template")); ok {
		s = settings(templateFlow)
	}
	if r.URL.Query().Get("template") == "welcome" {
		s = DraftSettings{Welcome: d.Welcome.Text, MenuPrompt: d.Menu.Text}
	}
	h.draftPage(w, r, 200, b, s, saved, "")
}

func (h *Handler) SaveDraft(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.draftPage(w, r, 422, b, DraftSettings{}, false, locale.T(r.Context(), "draft.error.definition"))
		return
	}
	labels, messages := r.PostForm["choice_label"], r.PostForm["choice_message"]
	s := DraftSettings{Welcome: r.PostForm.Get("welcome"), MenuPrompt: r.PostForm.Get("menu_prompt")}
	var err error
	if len(labels) != len(messages) || len(labels) > flow.MaxChoices {
		err = &flow.Invalid{Key: "draft.error.choices"}
	} else {
		for i, label := range labels {
			s.Choices = append(s.Choices, MenuMessage{Label: label, Message: messages[i]})
		}
	}
	d := s.Definition()
	template := r.PostForm.Get("template")
	if templateFlow, ok := templateDefinition(template); ok {
		d = templateFlow
		d.Welcome.Text, d.Menu.Text = s.Welcome, s.MenuPrompt
		d.Menu.Choices[0].Label = r.PostForm.Get("form_label")
		f := &d.Forms[0]
		f.Review, f.Acknowledgement = r.PostForm.Get("review_message"), r.PostForm.Get("acknowledgement")
		ql, qp, qr := r.PostForm["question_label"], r.PostForm["question_prompt"], r.PostForm["question_required"]
		if len(ql) != len(f.Questions) || len(qp) != len(f.Questions) || len(qr) != len(f.Questions) {
			err = &flow.Invalid{Key: "draft.error.definition"}
		} else {
			for i := range f.Questions {
				q := &f.Questions[i]
				q.Label, q.Prompt, q.Required = ql[i], qp[i], qr[i] == "yes"
				if qr[i] != "yes" && qr[i] != "no" {
					err = &flow.Invalid{Key: "draft.error.definition"}
				}
				if q.Type == "single_choice" {
					values := r.PostForm["question_options"]
					if len(values) != 1 {
						err = &flow.Invalid{Key: "draft.error.definition"}
					} else {
						q.Options = strings.Split(strings.ReplaceAll(values[0], "\r\n", "\n"), "\n")
						for j := range q.Options {
							q.Options[j] = strings.TrimSpace(q.Options[j])
						}
					}
				}
				if q.Type == "number" {
					min, max := r.PostForm["number_min"], r.PostForm["number_max"]
					if len(min) != 1 || len(max) != 1 {
						err = &flow.Invalid{Key: "draft.error.definition"}
					} else {
						q.Number = &flow.NumberRules{Min: strings.TrimSpace(min[0]), Max: strings.TrimSpace(max[0])}
					}
				}
			}
		}
		s = settings(d)
	} else if template != "" && template != "welcome" {
		err = &flow.Invalid{Key: "draft.error.definition"}
	}

	// The structured form contract shares validation with ordinary settings and
	// future adapters; there is no route that executes owner-supplied code.
	if values, supplied := r.PostForm["definition"]; supplied {
		if len(values) != 1 {
			err = &flow.Invalid{Key: "draft.error.definition"}
		} else {
			d, err = flow.Decode(values[0])
			if err == nil {
				s = settings(d)
			}
		}
	}
	if err == nil {
		err = h.service.SaveDraft(r.Context(), b.ID, d)
	}
	if err != nil {
		var invalid *flow.Invalid
		if errors.As(err, &invalid) {
			h.draftPage(w, r, 422, b, s, false, locale.T(r.Context(), invalid.Key))
			return
		}
		h.draftError(w, r, err)
		return
	}
	http.Redirect(w, r, b.URL()+"/draft?saved=1", http.StatusSeeOther)
}

func (h *Handler) draftPage(w http.ResponseWriter, r *http.Request, status int, b Bot, s DraftSettings, saved bool, message string) {
	u, _ := auth.UserFromContext(r.Context())
	h.render(w, r, status, DraftPage(u.DisplayName, request.CookieValue(r, auth.CSRFCookie), b, s, saved, r.URL.Query().Get("saved") == "1", message))
}

func (h *Handler) draftError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrNotFound) {
		web.RenderError(w, r, 404, "error.message.page.missing")
		return
	}
	h.failed(w, r)
}
