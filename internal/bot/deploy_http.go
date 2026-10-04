package bot

import (
	"errors"
	"net/http"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/web"
	"github.com/pooya79/Piko/internal/web/request"
)

func (h *Handler) Deploy(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	if r.ParseForm() != nil || r.PostForm.Get("operate") != "yes" {
		web.RenderError(w, r, 422, "activate.confirm")
		return
	}
	result, err := h.service.Deploy(r.Context(), b.ID, r.PostForm.Get("conflict"))
	status, key := http.StatusOK, "deploy.success"
	var invalid *flow.Invalid
	if err != nil {
		switch {
		case result.Version > 0:
			status, key = 503, "deploy.partial"
			if errors.Is(err, ErrWebhookConflict) {
				status = 409
			}
		case errors.Is(err, ErrBuilderBusy):
			status, key = 409, "deploy.busy"
		case errors.Is(err, ErrUnconnected), errors.Is(err, ErrDisconnected):
			status, key = 409, "deploy.connect"
		case errors.Is(err, ErrNoDraft), errors.As(err, &invalid):
			status, key = 422, "publish.error.draft"
		default:
			h.draftError(w, r, err)
			return
		}
	}
	// Fetch current delivery/pause state without inferring liveness from publication.
	result.Bot, err = h.service.Get(r.Context(), b.ID)
	if err != nil {
		h.draftError(w, r, err)
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	h.render(w, r, status, DeployPage(u.DisplayName, request.CookieValue(r, auth.CSRFCookie), result, key))
}
