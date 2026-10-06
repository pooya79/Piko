package bot

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot/flow"
	"github.com/pooya79/Piko/internal/web"
	"github.com/pooya79/Piko/internal/web/request"
)

func (h *Handler) ConfirmAction(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "proposalID"), 10, 64)
	if err != nil || id <= 0 {
		h.botError(w, r, ErrNotFound)
		return
	}
	if r.ParseForm() != nil {
		web.RenderError(w, r, 422, "activate.confirm")
		return
	}
	out, err := h.service.ConfirmAction(r.Context(), b.ID, id, r.PostForm.Get("operate") == "yes")
	status := 200
	var invalid *flow.Invalid
	if err != nil {
		switch {
		case errors.Is(err, ErrStaleAction):
			web.RenderError(w, r, 409, "action.stale")
			return
		case errors.Is(err, ErrAction):
			web.RenderError(w, r, 422, "activate.confirm")
			return
		case errors.Is(err, ErrBuilderBusy):
			web.RenderError(w, r, 409, "deploy.busy")
			return
		case errors.Is(err, ErrNoDraft), errors.As(err, &invalid):
			web.RenderError(w, r, 422, "publish.error.draft")
			return
		case errors.Is(err, ErrPauseUnavailable):
			web.RenderError(w, r, 409, "pause.unavailable")
			return
		case errors.Is(err, ErrUnconnected), errors.Is(err, ErrDisconnected):
			out.Key = "deploy.connect"
			out.Deployment.Bot = b
			status = 409
		default:
			h.botError(w, r, err)
			return
		}
	}
	if out.Key == "deploy.partial" {
		status = 503
	}
	if out.Key == "action.executing" {
		status = 409
	}
	u, _ := auth.UserFromContext(r.Context())
	csrf := request.CookieValue(r, auth.CSRFCookie)
	if out.Proposal.Action == "deploy" {
		h.render(w, r, status, DeployPage(u.DisplayName, csrf, out.Deployment, out.Key))
		return
	}
	h.render(w, r, status, ActionPage(u.DisplayName, csrf, out.Deployment.Bot, out.Key))
}
