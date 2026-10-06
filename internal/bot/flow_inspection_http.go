package bot

import (
	"encoding/json"
	"net/http"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/web"
	"github.com/pooya79/Piko/internal/web/request"
)

func (h *Handler) Flow(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	mode := r.URL.Query().Get("view")
	if mode != "" && mode != "draft" && mode != "published" {
		web.RenderError(w, r, 404, "error.message.page.missing")
		return
	}
	view, err := h.service.InspectFlow(r.Context(), b.ID, mode == "published")
	if err != nil {
		h.draftError(w, r, err)
		return
	}
	if r.URL.Path == b.URL()+"/flow/status" {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(struct {
			Revision int64 `json:"revision"`
		}{view.Revision})
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	h.render(w, r, 200, FlowPage(u.DisplayName, request.CookieValue(r, auth.CSRFCookie), b, view))
}
