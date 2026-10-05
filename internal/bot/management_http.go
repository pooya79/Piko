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
	h.settingsPage(w, r, http.StatusOK, b, b.Name, "")
}

func (h *Handler) settingsPage(w http.ResponseWriter, r *http.Request, status int, b Bot, name, key string) {
	u, _ := auth.UserFromContext(r.Context())
	h.render(w, r, status, SettingsPage(u.DisplayName, request.CookieValue(r, auth.CSRFCookie), b, name, key, r.URL.Query().Get("saved") == "1"))
}

func (h *Handler) DeleteForm(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if ok {
		h.deletePage(w, r, http.StatusOK, b, "")
	}
}

func (h *Handler) deletePage(w http.ResponseWriter, r *http.Request, status int, b Bot, key string) {
	u, _ := auth.UserFromContext(r.Context())
	h.render(w, r, status, DeletePage(u.DisplayName, request.CookieValue(r, auth.CSRFCookie), b, key))
}

func (h *Handler) Connection(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	h.connectionPage(w, r, http.StatusOK, b, "", false)
}

func (h *Handler) connectionPage(w http.ResponseWriter, r *http.Request, status int, b Bot, key string, invalid bool) {
	u, _ := auth.UserFromContext(r.Context())
	h.render(w, r, status, ConnectionPage(u.DisplayName, request.CookieValue(r, auth.CSRFCookie), b, r.URL.Query().Get("cleanup") == "unavailable", key, invalid))
}

func (h *Handler) DisconnectForm(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	if b.Unconnected || b.Disconnected {
		h.connectionPage(w, r, 409, b, "lifecycle.state.error", false)
		return
	}
	h.disconnectPage(w, r, http.StatusOK, b, "")
}

func (h *Handler) disconnectPage(w http.ResponseWriter, r *http.Request, status int, b Bot, key string) {
	u, _ := auth.UserFromContext(r.Context())
	h.render(w, r, status, DisconnectPage(u.DisplayName, request.CookieValue(r, auth.CSRFCookie), b, key))
}
