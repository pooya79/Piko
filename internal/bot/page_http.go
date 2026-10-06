package bot

import (
	"context"
	"errors"

	"github.com/go-chi/chi/v5"
	"github.com/pooya79/Piko/internal/locale"
	"github.com/pooya79/Piko/internal/web"
	"github.com/pooya79/Piko/internal/web/shell"
	"net/http"
	"strconv"
)

func flowPage(ctx context.Context, b Bot, titleKey string) shell.Page {
	var section shell.BotNavSection
	switch titleKey {
	case "flow.title":
		section = shell.BotFlow
	case "submission.title", "submission.detail":
		section = shell.BotSubmissions
	case "bot.connection.title", "bot.connect.title":
		section = shell.BotConnection
	case "bot.settings.title":
		section = shell.BotSettings
	}
	return shell.Page{Title: locale.T(ctx, titleKey), BotURL: b.URL(), BotSection: section, ActiveNav: shell.BotsNav, Breadcrumbs: []shell.Breadcrumb{{Label: locale.T(ctx, "workspace.bots"), URL: "/bots"}, {Label: b.Name, URL: b.URL()}, {Label: locale.T(ctx, titleKey)}}}
}

func (h *Handler) requestedBot(w http.ResponseWriter, r *http.Request) (Bot, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "botID"), 10, 64)
	if err != nil || id <= 0 {
		web.RenderError(w, r, 404, "error.message.page.missing")
		return Bot{}, false
	}
	b, err := h.service.Get(r.Context(), id)
	if err != nil {
		h.botError(w, r, err)
		return Bot{}, false
	}
	return b, true
}

func (h *Handler) botError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrNotFound) {
		web.RenderError(w, r, 404, "error.message.page.missing")
		return
	}
	h.failed(w, r)
}
