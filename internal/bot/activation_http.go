package bot

import (
	"errors"
	"net/http"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/locale"
	"github.com/pooya79/Piko/internal/web"
	"github.com/pooya79/Piko/internal/web/request"
)

func (h *Handler) Activation(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	a, err := h.service.InspectActivation(r.Context(), b.ID)
	if err != nil {
		h.activationError(w, r, err)
		return
	}
	h.activationPage(w, r, 200, a, "")
}
func (h *Handler) Activate(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	if r.ParseForm() != nil || r.PostForm.Get("operate") != "yes" {
		web.RenderError(w, r, 422, "activate.confirm")
		return
	}
	a, err := h.service.Activate(r.Context(), b.ID, r.PostForm.Get("conflict"))
	if errors.Is(err, ErrWebhookConflict) {
		h.activationPage(w, r, 409, a, "activate.changed")
		return
	}
	if err != nil {
		h.activationError(w, r, err)
		return
	}
	http.Redirect(w, r, b.URL(), http.StatusSeeOther)
}
func (h *Handler) activationPage(w http.ResponseWriter, r *http.Request, status int, a Activation, key string) {
	u, _ := auth.UserFromContext(r.Context())
	message := ""
	if key != "" {
		message = locale.T(r.Context(), key)
	}
	h.render(w, r, status, ActivationPage(u.DisplayName, request.CookieValue(r, auth.CSRFCookie), a, message))
}
func (h *Handler) activationError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrDisconnected):
		web.RenderError(w, r, 409, "lifecycle.state.error")
	case errors.Is(err, ErrNoDraft):
		web.RenderError(w, r, 422, "activate.publish")
	case errors.Is(err, ErrActivationBusy):
		web.RenderError(w, r, 409, "activate.busy")
	case errors.Is(err, telegram.ErrUnavailable), errors.Is(err, telegram.ErrCredentials), errors.Is(err, telegram.ErrRejected):
		web.RenderError(w, r, 503, "activate.error")
	default:
		h.draftError(w, r, err)
	}
}
