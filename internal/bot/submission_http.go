package bot

import (
	"github.com/go-chi/chi/v5"
	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/web"
	"github.com/pooya79/Piko/internal/web/request"
	"net/http"
	"strconv"
)

func (h *Handler) Submissions(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	var before int64
	if value := r.URL.Query().Get("before"); value != "" {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil || n < 1 {
			web.RenderError(w, r, http.StatusBadRequest, "error.message.page.missing")
			return
		}
		before = n
	}
	items, err := h.service.ListSubmissions(r.Context(), b.ID, before)
	if err != nil {
		h.draftError(w, r, err)
		return
	}
	var next int64
	if len(items) > 50 {
		items = items[:50]
		next = items[len(items)-1].ID
	}
	u, _ := auth.UserFromContext(r.Context())
	h.render(w, r, http.StatusOK, SubmissionsPage(u.DisplayName, request.CookieValue(r, auth.CSRFCookie), b, items, next))
}

func (h *Handler) Submission(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "submissionID"), 10, 64)
	if err != nil || id < 1 {
		web.RenderError(w, r, http.StatusNotFound, "error.message.page.missing")
		return
	}
	item, err := h.service.GetSubmission(r.Context(), b.ID, id)
	if err != nil {
		h.draftError(w, r, err)
		return
	}
	u, _ := auth.UserFromContext(r.Context())
	h.render(w, r, http.StatusOK, SubmissionPage(u.DisplayName, request.CookieValue(r, auth.CSRFCookie), b, item))
}
