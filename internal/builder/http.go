package builder

import (
	"errors"
	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot"
	"github.com/pooya79/Piko/internal/web"
	"github.com/pooya79/Piko/internal/web/request"
	"log/slog"
	"net/http"
	"strconv"
)

type Handler struct {
	service *Service
	bots    *bot.Service
	log     *slog.Logger
}

func NewHandler(service *Service, bots *bot.Service, log *slog.Logger) *Handler {
	return &Handler{service: service, bots: bots, log: log}
}

func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	bots, err := h.bots.List(r.Context())
	if err != nil {
		h.failed(w, r, err)
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	h.render(w, r, 200, IndexPage(u.DisplayName, request.CookieValue(r, auth.CSRFCookie), bots))
}

func (h *Handler) requestedBot(w http.ResponseWriter, r *http.Request) (bot.Bot, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "botID"), 10, 64)
	if err != nil || id < 1 {
		h.failed(w, r, bot.ErrNotFound)
		return bot.Bot{}, false
	}
	b, err := h.bots.Get(r.Context(), id)
	if err != nil {
		h.failed(w, r, err)
		return bot.Bot{}, false
	}
	return b, true
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	h.list(w, r, b, 200, "", "")
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, b bot.Bot, status int, title, key string) {
	chats, err := h.service.List(r.Context(), b.ID)
	if err != nil {
		h.failed(w, r, err)
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	h.render(w, r, status, ListPage(u.DisplayName, request.CookieValue(r, auth.CSRFCookie), b, chats, title, key))
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil || len(r.PostForm["title"]) != 1 {
		h.list(w, r, b, 422, "", "builder.title.error")
		return
	}
	chat, err := h.service.Create(r.Context(), b.ID, r.PostForm.Get("title"))
	if errors.Is(err, ErrTitle) {
		h.list(w, r, b, 422, r.PostForm.Get("title"), "builder.title.error")
		return
	}
	if err != nil {
		h.failed(w, r, err)
		return
	}
	http.Redirect(w, r, chat.URL(), http.StatusSeeOther)
}

func chatID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "chatID"), 10, 64)
	if err != nil || id < 1 {
		return 0, bot.ErrNotFound
	}
	return id, nil
}

func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	id, err := chatID(r)
	if err != nil {
		h.failed(w, r, err)
		return
	}
	history, err := h.service.History(r.Context(), b.ID, id)
	if err != nil {
		h.failed(w, r, err)
		return
	}
	draft, err := h.bots.LoadDraft(r.Context(), b.ID)
	if err != nil {
		h.failed(w, r, err)
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	h.render(w, r, 200, ChatPage(u.DisplayName, request.CookieValue(r, auth.CSRFCookie), b, history, draft.Revision))
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	id, err := chatID(r)
	if err == nil {
		err = h.service.Delete(r.Context(), b.ID, id)
	}
	if err != nil {
		h.failed(w, r, err)
		return
	}
	http.Redirect(w, r, b.URL()+"/chats", http.StatusSeeOther)
}

func (h *Handler) failed(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, bot.ErrNotFound) {
		web.RenderError(w, r, 404, "error.message.page.missing")
		return
	}
	h.log.ErrorContext(r.Context(), "Builder page unavailable")
	web.RenderError(w, r, 500, "error.message.server")
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, page templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := page.Render(r.Context(), w); err != nil {
		h.log.ErrorContext(r.Context(), "render Builder page failed")
	}
}
