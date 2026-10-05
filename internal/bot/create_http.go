package bot

import (
	"errors"
	"net/http"

	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/locale"
	"github.com/pooya79/Piko/internal/web/request"
)

func (h *Handler) CreateForm(w http.ResponseWriter, r *http.Request) {
	h.createPage(w, r, 200, "", "")
}

func (h *Handler) Rename(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requestedBot(w, r)
	if !ok {
		return
	}
	if r.ParseForm() != nil || len(r.PostForm["name"]) != 1 {
		http.Error(w, locale.T(r.Context(), "bot.create.name.error"), http.StatusUnprocessableEntity)
		return
	}
	if err := h.service.Rename(r.Context(), b.ID, r.PostForm.Get("name")); errors.Is(err, ErrBotName) {
		http.Error(w, locale.T(r.Context(), "bot.create.name.error"), http.StatusUnprocessableEntity)
		return
	} else if err != nil {
		h.failed(w, r)
		return
	}
	http.Redirect(w, r, b.URL(), http.StatusSeeOther)
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil || len(r.PostForm["name"]) != 1 {
		h.createPage(w, r, 422, "", "bot.create.name.error")
		return
	}
	name := r.PostForm.Get("name")
	b, err := h.service.Create(r.Context(), name)
	if errors.Is(err, ErrBotName) {
		h.createPage(w, r, 422, name, "bot.create.name.error")
		return
	}
	if err != nil {
		h.failed(w, r)
		return
	}
	http.Redirect(w, r, b.URL()+"/draft", http.StatusSeeOther)
}

func (h *Handler) createPage(w http.ResponseWriter, r *http.Request, status int, name, key string) {
	u, _ := auth.UserFromContext(r.Context())
	message := ""
	if key != "" {
		message = locale.T(r.Context(), key)
	}
	h.render(w, r, status, CreatePage(u.DisplayName, request.CookieValue(r, auth.CSRFCookie), name, message))
}
