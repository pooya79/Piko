package dashboard

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/web/request"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	user, _ := auth.UserFromContext(r.Context())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := Page(user.DisplayName, request.CookieValue(r, auth.CSRFCookie), time.Now().UTC().Format(time.RFC3339)).Render(r.Context(), w); err != nil {
		slog.ErrorContext(r.Context(), "render dashboard", "error", err)
	}
}
