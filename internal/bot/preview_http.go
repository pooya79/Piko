package bot

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot/runtime"
	"github.com/pooya79/Piko/internal/locale"
	"github.com/pooya79/Piko/internal/web/request"
)

func (h *Handler) PreviewLanding(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	_, saved, err := h.service.LoadDraft(r.Context(), b.ID)
	if err != nil {
		h.draftError(w, r, err)
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	h.render(w, r, 200, PreviewLandingPage(u.DisplayName, request.CookieValue(r, auth.CSRFCookie), b, saved))
}

func (h *Handler) StartPreview(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	p, err := h.service.StartPreview(r.Context(), b.ID)
	if errors.Is(err, ErrNoDraft) {
		u, _ := auth.UserFromContext(r.Context())
		h.render(w, r, 409, PreviewLandingPage(u.DisplayName, request.CookieValue(r, auth.CSRFCookie), b, false))
		return
	}
	if err != nil {
		h.draftError(w, r, err)
		return
	}
	http.Redirect(w, r, b.URL()+"/preview/"+p.ID, http.StatusSeeOther)
}

func (h *Handler) Preview(w http.ResponseWriter, r *http.Request) { h.previewPage(w, r, 200, "") }

func (h *Handler) ChoosePreview(w http.ResponseWriter, r *http.Request) {
	h.advancePreview(w, r, false)
}
func (h *Handler) RestartPreview(w http.ResponseWriter, r *http.Request) {
	h.advancePreview(w, r, true)
}

func (h *Handler) advancePreview(w http.ResponseWriter, r *http.Request, restart bool) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.previewPage(w, r, 422, locale.T(r.Context(), "preview.error.choice"))
		return
	}
	revision, err := strconv.ParseInt(r.PostForm.Get("revision"), 10, 64)
	if err != nil || revision < 1 {
		h.previewPage(w, r, 409, locale.T(r.Context(), "preview.error.stale"))
		return
	}
	var answer *string
	if values, ok := r.PostForm["answer"]; ok {
		if len(values) != 1 || r.PostForm.Get("choice") != "" {
			h.previewPage(w, r, 422, locale.T(r.Context(), "preview.error.choice"))
			return
		}
		answer = &values[0]
	}
	err = h.service.AdvancePreview(r.Context(), b.ID, chi.URLParam(r, "previewID"), revision, r.PostForm.Get("choice"), restart, answer)
	switch {
	case errors.Is(err, ErrStalePreview):
		h.previewPage(w, r, 409, locale.T(r.Context(), "preview.error.stale"))
	case errors.Is(err, runtime.ErrChoice):
		h.previewPage(w, r, 422, locale.T(r.Context(), "preview.error.choice"))
	case err != nil:
		h.draftError(w, r, err)
	default:
		http.Redirect(w, r, b.URL()+"/preview/"+chi.URLParam(r, "previewID"), http.StatusSeeOther)
	}
}

func (h *Handler) previewPage(w http.ResponseWriter, r *http.Request, status int, message string) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	p, err := h.service.GetPreview(r.Context(), b.ID, chi.URLParam(r, "previewID"))
	if err != nil {
		h.draftError(w, r, err)
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	h.render(w, r, status, PreviewPage(u.DisplayName, request.CookieValue(r, auth.CSRFCookie), b, p, message))
}
