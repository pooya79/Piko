package bot

import (
	"errors"
	"net/http"

	"github.com/pooya79/Piko/internal/web"
)

func (h *Handler) Pause(w http.ResponseWriter, r *http.Request) {
	h.setPaused(w, r, true)
}

func (h *Handler) Resume(w http.ResponseWriter, r *http.Request) {
	h.setPaused(w, r, false)
}

func (h *Handler) setPaused(w http.ResponseWriter, r *http.Request, paused bool) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	if err := h.service.SetPaused(r.Context(), b.ID, paused); err != nil {
		if errors.Is(err, ErrPauseUnavailable) {
			web.RenderError(w, r, http.StatusConflict, "pause.unavailable")
		} else {
			h.draftError(w, r, err)
		}
		return
	}
	http.Redirect(w, r, b.URL(), http.StatusSeeOther)
}
