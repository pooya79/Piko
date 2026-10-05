package builder

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot"
	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/locale"
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

type ChatView struct {
	Selection            Selection
	Canvas               flow.Canvas
	Revision             int64
	Enabled              bool
	Allowance            Allowance
	Message, FeedbackKey string
	RequestKey           string
	Chats                []Chat
	DraftKey             string
	Bots                 []bot.Bot
	ActiveChat           Chat
	ActiveRun            Run
}

func NewHandler(service *Service, bots *bot.Service, log *slog.Logger) *Handler {
	return &Handler{service: service, bots: bots, log: log}
}

func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	h.index(w, r, 200, "", "")
}

func (h *Handler) index(w http.ResponseWriter, r *http.Request, status int, message, key string) {
	chats, err := h.service.SavedChats(r.Context())
	if err != nil {
		h.failed(w, r, err)
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	bots, err := h.bots.List(r.Context())
	if err != nil {
		h.failed(w, r, err)
		return
	}
	requestKey := r.PostForm.Get("request_key")
	if requestKey == "" {
		requestKey = "welcome:" + rand.Text()
	}
	view := ChatView{Enabled: h.service.Enabled(), Message: message, FeedbackKey: key, DraftKey: strconv.FormatInt(u.ID, 10) + ":new", RequestKey: requestKey, Bots: bots}
	csrf := request.CookieValue(r, auth.CSRFCookie)
	if r.Header.Get("X-Piko-Studio") == "fragment" {
		h.render(w, r, status, indexContent(csrf, chats, view))
		return
	}
	h.render(w, r, status, IndexPage(u.DisplayName, csrf, chats, view))
}

func (h *Handler) requestedBot(w http.ResponseWriter, r *http.Request) (bot.Bot, bool) {
	if chi.URLParam(r, "botID") == "" {
		if chi.URLParam(r, "chatID") == "" {
			return bot.Bot{}, true
		}
		id, err := chatID(r)
		if err != nil {
			h.failed(w, r, err)
			return bot.Bot{}, false
		}
		chat, err := h.service.ResolveChat(r.Context(), id)
		if err != nil {
			h.failed(w, r, err)
			return bot.Bot{}, false
		}
		if chat.BotID == 0 {
			return bot.Bot{}, true
		}
		b, err := h.bots.InspectDeployment(r.Context(), chat.BotID)
		if err != nil {
			h.failed(w, r, err)
			return bot.Bot{}, false
		}
		return b, true
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "botID"), 10, 64)
	if err != nil || id < 1 {
		h.failed(w, r, bot.ErrNotFound)
		return bot.Bot{}, false
	}
	b, err := h.bots.InspectDeployment(r.Context(), id)
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
	if err := r.ParseForm(); err != nil || (b.ID != 0 && len(r.PostForm["title"]) != 1) {
		if b.ID == 0 {
			h.index(w, r, 422, "", "builder.message.error")
		} else {
			h.list(w, r, b, 422, "", "builder.title.error")
		}
		return
	}
	if len(r.PostForm["message"]) > 1 || len(r.PostForm["request_key"]) > 1 {
		h.index(w, r, 422, "", "builder.message.error")
		return
	}
	title := r.PostForm.Get("title")
	if b.ID == 0 {
		title = locale.T(r.Context(), "piko.chat.title")
	}
	if len(r.PostForm["message"]) == 1 {
		if b.ID != 0 {
			h.list(w, r, b, 422, title, "builder.message.error")
			return
		}
		message := r.PostForm.Get("message")
		chat, err := h.service.StartConversation(r.Context(), title, message, r.PostForm.Get("request_key"))
		if err == nil {
			if r.Header.Get("X-Piko-Studio") == "fragment" {
				h.sent(w, r, b, chat.ID, message, nil)
			} else {
				http.Redirect(w, r, chat.URL(), http.StatusSeeOther)
			}
		} else if chat.ID != 0 {
			h.sent(w, r, b, chat.ID, message, err)
		} else if errors.Is(err, ErrMessage) {
			h.index(w, r, 422, message, "builder.message.error")
		} else {
			h.failed(w, r, err)
		}
		return
	}
	chat, err := h.service.Create(r.Context(), b.ID, title)
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
	h.detail(w, r, b, id, 200, "", "")
}

// Status only observes existing work; reconnecting never goes through admission.
func (h *Handler) Status(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	id, err := chatID(r)
	if err != nil {
		h.failed(w, r, err)
		return
	}
	run, err := h.service.Status(r.Context(), b.ID, id)
	if err != nil {
		h.failed(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(struct {
		ID     int64     `json:"id"`
		Status RunStatus `json:"status"`
	}{run.ID, run.Status})
}

func (h *Handler) detail(w http.ResponseWriter, r *http.Request, b bot.Bot, id int64, status int, message, key string) {
	history, err := h.service.History(r.Context(), b.ID, id)
	if err != nil {
		h.failed(w, r, err)
		return
	}
	if history.Chat.BotID != 0 {
		b = history.Bot
	}
	var revision int64
	var canvas flow.Canvas
	if b.ID != 0 {
		draft := history.Draft
		revision = draft.Revision
		if revision > 0 {
			canvas = flow.ProjectCanvas(draft.Definition)
		}
	}
	u, _ := auth.UserFromContext(r.Context())
	allowance, err := h.service.Allowance(r.Context())
	if err != nil {
		h.failed(w, r, err)
		return
	}
	requestKey := r.PostForm.Get("request_key")
	if status == http.StatusOK {
		requestKey = ""
	}
	if requestKey == "" {
		requestKey = strconv.FormatInt(id, 10) + ":" + strconv.FormatInt(history.LatestRun().ID, 10)
	}
	chats, err := h.service.SavedChats(r.Context())
	if err != nil {
		h.failed(w, r, err)
		return
	}
	view := ChatView{Canvas: canvas, Revision: revision, Enabled: h.service.Enabled(), Allowance: allowance, Message: message, FeedbackKey: key, RequestKey: requestKey, Chats: chats, DraftKey: strconv.FormatInt(u.ID, 10) + ":" + strconv.FormatInt(id, 10)}
	if b.ID != 0 {
		view.ActiveChat, err = h.service.ActiveChat(r.Context(), b.ID)
		if err != nil {
			h.failed(w, r, err)
			return
		}
		if view.ActiveChat.ID != 0 {
			view.ActiveRun, err = h.service.Status(r.Context(), b.ID, view.ActiveChat.ID)
			if err != nil {
				h.failed(w, r, err)
				return
			}
		}
	}
	if status != http.StatusOK {
		view.Selection.Key = r.PostForm.Get("selected_block")
		view.Selection.Revision, _ = strconv.ParseInt(r.PostForm.Get("selected_revision"), 10, 64)
	}
	view.Bots, err = h.bots.List(r.Context())
	if err != nil {
		h.failed(w, r, err)
		return
	}
	csrf := request.CookieValue(r, auth.CSRFCookie)
	if r.Header.Get("X-Piko-Studio") == "fragment" {
		h.render(w, r, status, chatContent(csrf, b, history, view))
		return
	}
	h.render(w, r, status, ChatPage(u.DisplayName, csrf, b, history, view))
}

func (h *Handler) Send(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	id, err := chatID(r)
	if err != nil {
		h.failed(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil || len(r.PostForm["message"]) != 1 || len(r.PostForm["request_key"]) > 1 {
		h.detail(w, r, b, id, 422, "", "builder.message.error")
		return
	}
	message := r.PostForm.Get("message")
	selection := Selection{Key: r.PostForm.Get("selected_block")}
	if len(r.PostForm["selected_block"]) > 1 || len(r.PostForm["selected_revision"]) > 1 {
		h.detail(w, r, b, id, 422, message, "builder.message.error")
		return
	}
	if selection.Key != "" || r.PostForm.Get("selected_revision") != "" {
		selection.Revision, err = strconv.ParseInt(r.PostForm.Get("selected_revision"), 10, 64)
		if err != nil || selection.Revision < 1 || selection.Key == "" || len(selection.Key) > 1024 {
			h.detail(w, r, b, id, 422, message, "builder.message.error")
			return
		}
	}
	err = h.service.SendSelectedRequest(r.Context(), b.ID, id, message, r.PostForm.Get("request_key"), selection)
	h.sent(w, r, b, id, message, err)
}

func (h *Handler) Retry(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	id, err := chatID(r)
	if err != nil {
		h.failed(w, r, err)
		return
	}
	runID, err := strconv.ParseInt(chi.URLParam(r, "runID"), 10, 64)
	if err != nil || runID <= 0 {
		h.failed(w, r, bot.ErrNotFound)
		return
	}
	h.sent(w, r, b, id, "", h.service.Retry(r.Context(), b.ID, id, runID))
}

func (h *Handler) Stop(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	id, err := chatID(r)
	if err != nil {
		h.failed(w, r, err)
		return
	}
	runID, err := strconv.ParseInt(chi.URLParam(r, "runID"), 10, 64)
	if err != nil || runID < 1 {
		h.failed(w, r, bot.ErrNotFound)
		return
	}
	if err := h.service.StopRun(r.Context(), b.ID, id, runID); err != nil {
		h.failed(w, r, err)
		return
	}
	h.updated(w, r, b, id)
}

func (h *Handler) Undo(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	id, err := chatID(r)
	if err != nil {
		h.failed(w, r, err)
		return
	}
	runID, err := strconv.ParseInt(chi.URLParam(r, "runID"), 10, 64)
	if err != nil || runID < 1 {
		h.failed(w, r, bot.ErrNotFound)
		return
	}
	if err := h.service.Undo(r.Context(), b.ID, id, runID); errors.Is(err, ErrUndo) {
		h.detail(w, r, b, id, http.StatusConflict, "", "builder.undo.unavailable")
		return
	} else if err != nil {
		h.failed(w, r, err)
		return
	}
	h.updated(w, r, b, id)
}

func (h *Handler) sent(w http.ResponseWriter, r *http.Request, b bot.Bot, id int64, message string, err error) {
	switch {
	case errors.Is(err, ErrSelection):
		h.detail(w, r, b, id, 409, message, "flow.stale")
	case errors.Is(err, ErrRetry):
		h.detail(w, r, b, id, 409, "", "builder.retry.unavailable")
	case errors.Is(err, ErrMessage):
		h.detail(w, r, b, id, 422, message, "builder.message.error")
	case errors.Is(err, ErrBusy):
		key := "builder.busy"
		if b.ID == 0 {
			key = "piko.chat.busy"
		}
		h.detail(w, r, b, id, 409, message, key)
	case errors.Is(err, ErrDailyLimit):
		h.detail(w, r, b, id, 429, message, "builder.limit")
	case errors.Is(err, ErrUnavailable):
		key := "builder.unavailable"
		if b.ID == 0 {
			key = "piko.chat.unavailable"
		}
		h.detail(w, r, b, id, 503, message, key)
	case err != nil:
		h.failed(w, r, err)
	default:
		w.Header().Set("X-Piko-Accepted", "true")
		h.updated(w, r, b, id)
	}
}

// Enhanced forms share the same authorized committed view as ordinary GETs.
func (h *Handler) updated(w http.ResponseWriter, r *http.Request, b bot.Bot, id int64) {
	if r.Header.Get("X-Piko-Studio") == "fragment" {
		h.detail(w, r, b, id, http.StatusOK, "", "")
		return
	}
	http.Redirect(w, r, (Chat{BotID: b.ID, ID: id}).URL(), http.StatusSeeOther)
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
	target := "/builder"
	if b.ID != 0 {
		target = b.URL() + "/chats"
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
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
