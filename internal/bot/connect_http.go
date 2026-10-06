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

func (h *Handler) ConnectExistingForm(w http.ResponseWriter, r *http.Request) {
	b, ok := h.unconnectedBot(w, r)
	if ok {
		h.existingConnection(w, r, 200, b, "", false, "")
	}
}

func (h *Handler) ConnectExisting(w http.ResponseWriter, r *http.Request) {
	b, ok := h.unconnectedBot(w, r)
	if !ok {
		return
	}
	if r.ParseForm() != nil || len(r.PostForm["token"]) != 1 {
		h.existingConnection(w, r, 422, b, "bot.error.token", true, "")
		return
	}
	connected, err := h.service.ConnectExisting(r.Context(), b.ID, r.PostForm.Get("token"))
	if err == nil {
		http.Redirect(w, r, connected.URL(), http.StatusSeeOther)
		return
	}
	var duplicate *DuplicateIdentity
	switch {
	case errors.Is(err, telegram.ErrCredentials):
		h.existingConnection(w, r, 422, b, "bot.error.token", true, "")
	case errors.Is(err, telegram.ErrUnavailable), errors.Is(err, telegram.ErrRejected), errors.Is(err, telegram.ErrForbidden):
		h.existingConnection(w, r, 503, b, "bot.error.telegram", false, "")
	case errors.As(err, &duplicate):
		key, existingURL := "bot.error.exists", ""
		if duplicate.Existing.ID > 0 {
			key, existingURL = "bot.connect.duplicate", duplicate.Existing.URL()
		}
		h.existingConnection(w, r, 409, b, key, false, existingURL)
	case errors.Is(err, ErrConnected):
		web.RenderError(w, r, 409, "lifecycle.state.error")
	case errors.Is(err, ErrNotFound):
		h.botError(w, r, err)
	default:
		h.log.ErrorContext(r.Context(), "Bot connection could not be saved")
		h.existingConnection(w, r, 500, b, "bot.error.save", false, "")
	}
}

func (h *Handler) unconnectedBot(w http.ResponseWriter, r *http.Request) (Bot, bool) {
	b, ok := h.requestedBot(w, r)
	if ok && !b.Unconnected {
		web.RenderError(w, r, 409, "lifecycle.state.error")
		return Bot{}, false
	}
	return b, ok
}

func (h *Handler) existingConnection(w http.ResponseWriter, r *http.Request, status int, b Bot, key string, invalid bool, existingURL string) {
	u, _ := auth.UserFromContext(r.Context())
	message := ""
	if key != "" {
		message = locale.T(r.Context(), key)
	}
	h.render(w, r, status, ConnectExistingPage(u.DisplayName, request.CookieValue(r, auth.CSRFCookie), b, message, invalid, existingURL))
}
