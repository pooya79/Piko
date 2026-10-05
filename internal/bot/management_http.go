package bot

import (
	"net/http"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/web/request"
)

func (h *Handler) Settings(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	h.render(w, r, http.StatusOK, SettingsPage(u.DisplayName, request.CookieValue(r, auth.CSRFCookie), b, r.URL.Query().Get("cleanup") == "unavailable"))
}

func (h *Handler) Connection(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	h.render(w, r, http.StatusOK, ConnectionPage(u.DisplayName, request.CookieValue(r, auth.CSRFCookie), b, r.URL.Query().Get("cleanup") == "unavailable"))
}
