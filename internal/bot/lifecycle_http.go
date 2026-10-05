package bot

import (
	"errors"
	"net/http"

	"github.com/pooya79/Piko/internal/bot/telegram"
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
		h.connectionPage(w, r, 422, b, "bot.error.token", true)
		return
	}
	warning, err := h.service.ReplaceToken(r.Context(), b.ID, r.PostForm.Get("token"), reconnect)
	if err != nil {
		h.credentialFailure(w, r, b, err)
		return
	}
	h.lifecycleRedirect(w, r, b.URL(), warning)
}

func (h *Handler) credentialFailure(w http.ResponseWriter, r *http.Request, b Bot, err error) {
	status, key, invalid := 500, "bot.error.save", false
	switch {
	case errors.Is(err, ErrNotFound):
		h.draftError(w, r, err)
		return
	case errors.Is(err, telegram.ErrCredentials):
		status, key, invalid = 422, "bot.error.token", true
	case errors.Is(err, ErrIdentity):
		status, key, invalid = 422, "lifecycle.identity.error", true
	case errors.Is(err, ErrUnconnected), errors.Is(err, ErrDisconnected):
		status, key = 409, "lifecycle.state.error"
	case errors.Is(err, ErrActivationBusy):
		status, key = 409, "activate.busy"
	case errors.Is(err, telegram.ErrUnavailable), errors.Is(err, telegram.ErrRejected), errors.Is(err, telegram.ErrForbidden):
		status, key = 503, "bot.error.telegram"
	default:
		h.log.ErrorContext(r.Context(), "Bot credentials could not be saved")
	}
	// Choose controls using current state after any concurrent lifecycle action.
	current, readErr := h.service.Get(r.Context(), b.ID)
	if readErr != nil {
		h.draftError(w, r, readErr)
		return
	}
	h.connectionPage(w, r, status, current, key, invalid)
}
func (h *Handler) Disconnect(w http.ResponseWriter, r *http.Request) { h.removeBot(w, r, false) }
func (h *Handler) DeleteBot(w http.ResponseWriter, r *http.Request)  { h.removeBot(w, r, true) }
func (h *Handler) removeBot(w http.ResponseWriter, r *http.Request, deleteData bool) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	if deleteData && (r.ParseForm() != nil || len(r.PostForm["confirm_delete"]) != 1 || r.PostForm.Get("confirm_delete") != "yes") {
		h.deletePage(w, r, 422, b, "lifecycle.delete.confirm")
		return
	}
	warning, err := h.service.Disconnect(r.Context(), b.ID, deleteData)
	if err != nil {
		if deleteData {
			if errors.Is(err, ErrNotFound) {
				h.draftError(w, r, err)
				return
			}
			status, key := 500, "bot.error.save"
			if errors.Is(err, ErrActivationBusy) {
				status, key = 409, "activate.busy"
			} else {
				h.log.ErrorContext(r.Context(), "Bot deletion could not be saved")
			}
			h.deletePage(w, r, status, b, key)
		} else {
			h.disconnectFailure(w, r, err)
		}
		return
	}
	destination := b.URL()
	if deleteData {
		destination = "/bots"
	}
	h.lifecycleRedirect(w, r, destination, warning)
}

func (h *Handler) disconnectFailure(w http.ResponseWriter, r *http.Request, err error) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	if b.Unconnected || b.Disconnected {
		h.connectionPage(w, r, 409, b, "lifecycle.state.error", false)
		return
	}
	status, key := 500, "lifecycle.disconnect.error"
	if errors.Is(err, ErrActivationBusy) {
		status, key = 409, "activate.busy"
	} else {
		h.log.ErrorContext(r.Context(), "Bot disconnection could not be saved")
	}
	h.disconnectPage(w, r, status, b, key)
}
func (h *Handler) lifecycleRedirect(w http.ResponseWriter, r *http.Request, destination string, warning bool) {
	if warning {
		destination += "?cleanup=unavailable"
	}
	http.Redirect(w, r, destination, http.StatusSeeOther)
}
