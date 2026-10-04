package bot

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot/telegram"
	"github.com/pooya79/Piko/internal/locale"
	"github.com/pooya79/Piko/internal/web"
	"github.com/pooya79/Piko/internal/web/request"
	"github.com/pooya79/Piko/internal/web/shell"
)

type Handler struct {
	service *Service
	log     *slog.Logger
}

func NewHandler(service *Service, log *slog.Logger) *Handler { return &Handler{service, log} }

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	bots, err := h.service.List(r.Context())
	if err != nil {
		h.failed(w, r)
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	h.render(w, r, 200, ListPage(u.DisplayName, request.CookieValue(r, auth.CSRFCookie), bots, r.URL.Query().Get("cleanup") == "unavailable"))
}
func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "botID"), 10, 64)
	if err != nil || id <= 0 {
		web.RenderError(w, r, http.StatusNotFound, "error.message.page.missing")
		return
	}
	b, err := h.service.InspectDeployment(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		web.RenderError(w, r, http.StatusNotFound, "error.message.page.missing")
		return
	}
	if err != nil {
		h.failed(w, r)
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	h.render(w, r, 200, DetailPage(u.DisplayName, request.CookieValue(r, auth.CSRFCookie), b, r.URL.Query().Get("cleanup") == "unavailable"))
}
func (h *Handler) ConnectForm(w http.ResponseWriter, r *http.Request) {
	h.connection(w, r, 200, "", false)
}
func (h *Handler) Connect(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.connection(w, r, 422, "bot.error.token", true)
		return
	}
	b, err := h.service.Connect(r.Context(), r.PostForm.Get("token"))
	if err != nil {
		switch {
		case errors.Is(err, telegram.ErrCredentials):
			h.connection(w, r, 422, "bot.error.token", true)
		case errors.Is(err, telegram.ErrUnavailable):
			h.connection(w, r, 503, "bot.error.telegram", false)
		case errors.Is(err, ErrExists):
			h.connection(w, r, 409, "bot.error.exists", false)
		default:
			// Never log arbitrary DB or transport errors containing credential data.
			h.log.ErrorContext(r.Context(), "Bot connection could not be saved")
			h.connection(w, r, 500, "bot.error.save", false)
		}
		return
	}
	http.Redirect(w, r, b.URL(), http.StatusSeeOther)
}
func (h *Handler) connection(w http.ResponseWriter, r *http.Request, status int, key string, invalid bool) {
	u, _ := auth.UserFromContext(r.Context())
	message := ""
	if key != "" {
		message = locale.T(r.Context(), key)
	}
	h.render(w, r, status, ConnectPage(u.DisplayName, request.CookieValue(r, auth.CSRFCookie), message, invalid))
}
func (h *Handler) failed(w http.ResponseWriter, r *http.Request) {
	h.log.ErrorContext(r.Context(), "Bot page unavailable")
	web.RenderError(w, r, 500, "error.message.server")
}
func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, page templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := page.Render(r.Context(), w); err != nil {
		h.log.ErrorContext(r.Context(), "render Bot page failed")
	}
}
func page(ctxTitle string) shell.Page {
	return shell.Page{Title: ctxTitle, ActiveNav: shell.BotsNav}
}
