package bot

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/locale"
	"github.com/pooya79/Piko/internal/web"
	"github.com/pooya79/Piko/internal/web/request"
	"github.com/pooya79/Piko/internal/web/shell"
)

func flowPage(ctx context.Context, b Bot, titleKey string) shell.Page {
	return shell.Page{Title: locale.T(ctx, titleKey), ActiveNav: shell.BotsNav, Breadcrumbs: []shell.Breadcrumb{{Label: locale.T(ctx, "workspace.bots"), URL: "/bots"}, {Label: b.Name, URL: b.URL()}, {Label: locale.T(ctx, titleKey)}}}
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
	h.draftPage(w, r, 200, b, settings(d), saved, "")
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
