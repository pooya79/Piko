package dashboard

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot"
	"github.com/pooya79/Piko/internal/web"
	"github.com/pooya79/Piko/internal/web/request"
)

func NewHandler(bots *bot.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		owned, err := bots.List(r.Context())
		if err != nil {
			web.RenderError(w, r, 500, "error.message.server")
			return
		}
		user, _ := auth.UserFromContext(r.Context())
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := Page(user.DisplayName, request.CookieValue(r, auth.CSRFCookie), time.Now().UTC().Format(time.RFC3339), owned).Render(r.Context(), w); err != nil {
			slog.ErrorContext(r.Context(), "render dashboard", "error", err)
		}
	}
}
