package bot

import (
	"errors"
	"net/http"

	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/web"
)

func (h *Handler) ReplaceToken(w http.ResponseWriter, r *http.Request) {
	h.credentialsMutation(w, r, false)
}
func (h *Handler) Reconnect(w http.ResponseWriter, r *http.Request) {
	h.credentialsMutation(w, r, true)
}
func (h *Handler) credentialsMutation(w http.ResponseWriter, r *http.Request, reconnect bool) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	if r.ParseForm() != nil || len(r.PostForm["token"]) != 1 {
		h.lifecycleError(w, r, telegram.ErrCredentials)
		return
	}
	warning, err := h.service.ReplaceToken(r.Context(), b.ID, r.PostForm.Get("token"), reconnect)
	if err != nil {
		h.lifecycleError(w, r, err)
		return
	}
	h.lifecycleRedirect(w, r, b.URL(), warning)
}
func (h *Handler) Disconnect(w http.ResponseWriter, r *http.Request) { h.removeBot(w, r, false) }
func (h *Handler) DeleteBot(w http.ResponseWriter, r *http.Request)  { h.removeBot(w, r, true) }
func (h *Handler) removeBot(w http.ResponseWriter, r *http.Request, deleteData bool) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	if deleteData && (r.ParseForm() != nil || len(r.PostForm["confirm_delete"]) != 1 || r.PostForm.Get("confirm_delete") != "yes") {
		web.RenderError(w, r, 422, "lifecycle.delete.confirm")
		return
	}
	warning, err := h.service.Disconnect(r.Context(), b.ID, deleteData)
	if err != nil {
		h.lifecycleError(w, r, err)
		return
	}
	destination := b.URL()
	if deleteData {
		destination = "/bots"
	}
	h.lifecycleRedirect(w, r, destination, warning)
}
func (h *Handler) lifecycleRedirect(w http.ResponseWriter, r *http.Request, destination string, warning bool) {
	if warning {
		destination += "?cleanup=unavailable"
	}
	http.Redirect(w, r, destination, http.StatusSeeOther)
}
func (h *Handler) lifecycleError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrIdentity):
		web.RenderError(w, r, 422, "lifecycle.identity.error")
	case errors.Is(err, ErrDisconnected):
		web.RenderError(w, r, 409, "lifecycle.state.error")
	case errors.Is(err, ErrActivationBusy):
		web.RenderError(w, r, 409, "activate.busy")
	case errors.Is(err, telegram.ErrCredentials):
		web.RenderError(w, r, 422, "bot.error.token")
	case errors.Is(err, telegram.ErrUnavailable), errors.Is(err, telegram.ErrRejected):
		web.RenderError(w, r, 503, "bot.error.telegram")
	default:
		h.draftError(w, r, err)
	}
}
