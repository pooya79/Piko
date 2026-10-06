package httpapp

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/bot"
	"github.com/pooya79/Piko/internal/builder"
	"github.com/pooya79/Piko/internal/dashboard"
	webx "github.com/pooya79/Piko/internal/web"
	"github.com/pooya79/Piko/internal/web/shell"
)

const (
	loginRateLimit         = 10
	loginRateWindow        = time.Minute
	registrationRateLimit  = 5
	registrationRateWindow = time.Hour
)

// Router loads sessions before CSRF checks so the latter can choose session-bound tokens.
func Router(db *sql.DB, mw webx.Middleware, limiter *webx.RateLimiter, ah *auth.Handler, bots *bot.Service, builders *builder.Service) http.Handler {
	bh := bot.NewHandler(bots, mw.Log)
	if builders == nil {
		builders = builder.NewService(builder.NewRepository(db), bots)
	}
	builderHandler := builder.NewHandler(builders, bots, mw.Log)
	r := chi.NewRouter()
	// The inner recovery sees the account locale; the outer one also covers
	// failures while loading the request locale or session.
	r.Use(mw.RequestID, mw.Recover, mw.Logging, mw.Security, mw.LimitBody, mw.RequestLocale, mw.Session, mw.Recover, mw.CSRF)
	// Only unmatched routes use this presentation; registered health and static
	// handlers retain their machine-facing responses and failure semantics.
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		webx.RenderError(w, r, http.StatusNotFound, "error.message.page.missing")
	})
	r.MethodNotAllowed(func(w http.ResponseWriter, req *http.Request) {
		// A custom chi responder replaces its default Allow-header handling.
		for _, method := range []string{http.MethodConnect, http.MethodDelete, http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPatch, http.MethodPost, http.MethodPut, http.MethodTrace} {
			if r.Match(chi.NewRouteContext(), method, req.URL.Path) {
				w.Header().Add("Allow", method)
			}
		}
		webx.RenderError(w, req, http.StatusMethodNotAllowed, "error.message.method")
	})
	r.Get("/health/live", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	r.Get("/health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if e := db.PingContext(ctx); e != nil {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ready"}`))
	})
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := auth.UserFromContext(r.Context()); ok {
			http.Redirect(w, r, "/dashboard", http.StatusFound)
		} else {
			http.Redirect(w, r, "/login", http.StatusFound)
		}
	})
	r.Group(func(r chi.Router) {
		r.With(limiter.Middleware("login", loginRateLimit, loginRateWindow)).Post("/login", ah.Login)
		r.Get("/login", ah.LoginForm)
		r.With(limiter.MiddlewareStrict("register", registrationRateLimit, registrationRateWindow)).Post("/register", ah.Register)
		r.Get("/register", ah.RegisterForm)
		r.Post("/logout", ah.Logout)
	})
	r.Group(func(r chi.Router) {
		r.Use(mw.RequireAuth)
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				owned, err := bots.List(req.Context())
				if err != nil {
					webx.RenderError(w, req, 500, "error.message.server")
					return
				}
				next.ServeHTTP(w, req.WithContext(shell.WithNavigation(req.Context(), bot.Navigation(owned))))
			})
		})
		r.Get("/account", ah.Profile)
		r.Get("/dashboard", dashboard.NewHandler(bots))
		r.Get("/builder", builderHandler.Index)
		r.Post("/chats", builderHandler.Create)
		r.Get("/chats/{chatID}", builderHandler.Detail)
		r.Get("/chats/{chatID}/status", builderHandler.Status)
		r.Get("/chats/{chatID}/stream", builderHandler.Stream)
		r.Post("/chats/{chatID}/messages", builderHandler.Send)
		r.Post("/chats/{chatID}/runs/{runID}/retry", builderHandler.Retry)
		r.Post("/chats/{chatID}/runs/{runID}/stop", builderHandler.Stop)
		r.Post("/chats/{chatID}/runs/{runID}/undo", builderHandler.Undo)
		r.Get("/bots/{botID}/chats", builderHandler.List)
		r.Post("/bots/{botID}/proposals/{proposalID}/confirm", bh.ConfirmAction)
		r.Post("/bots/{botID}/chats", builderHandler.Create)
		r.Get("/bots/{botID}/chats/{chatID}", builderHandler.Detail)
		r.Get("/bots/{botID}/chats/{chatID}/status", builderHandler.Status)
		r.Get("/bots/{botID}/chats/{chatID}/stream", builderHandler.Stream)
		r.Post("/bots/{botID}/chats/{chatID}/messages", builderHandler.Send)
		r.Post("/bots/{botID}/chats/{chatID}/runs/{runID}/retry", builderHandler.Retry)
		r.Post("/bots/{botID}/chats/{chatID}/runs/{runID}/stop", builderHandler.Stop)
		r.Post("/bots/{botID}/chats/{chatID}/runs/{runID}/undo", builderHandler.Undo)
		r.Get("/bots", bh.List)
		r.Get("/bots/new", bh.ConnectForm)
		r.With(limiter.MiddlewareStrict("bot-create", 10, time.Minute)).Post("/bots/new", bh.Create)
		r.Get("/bots/connect", bh.ConnectForm)
		r.With(limiter.MiddlewareStrict("bot-connect", 10, time.Minute)).Post("/bots/connect", bh.Connect)
		r.Get("/bots/{botID}", bh.Detail)
		r.Get("/bots/{botID}/created", bh.Created)
		r.Get("/bots/{botID}/settings", bh.Settings)
		r.Get("/bots/{botID}/settings/delete", bh.DeleteForm)
		r.Get("/bots/{botID}/connection", bh.Connection)
		r.Get("/bots/{botID}/connection/disconnect", bh.DisconnectForm)
		r.Get("/bots/{botID}/studio", builderHandler.Studio)
		r.Get("/bots/{botID}/flow", bh.Flow)
		r.Get("/bots/{botID}/flow/status", bh.Flow)
		r.Get("/bots/{botID}/connect", bh.ConnectExistingForm)
		r.With(limiter.MiddlewareStrict("bot-connect", 10, time.Minute)).Post("/bots/{botID}/connect", bh.ConnectExisting)
		r.With(limiter.MiddlewareStrict("bot-credentials", 10, time.Minute)).Post("/bots/{botID}/replace-token", bh.ReplaceToken)
		r.With(limiter.MiddlewareStrict("bot-credentials", 10, time.Minute)).Post("/bots/{botID}/reconnect", bh.Reconnect)
		r.Post("/bots/{botID}/disconnect", bh.Disconnect)
		r.Post("/bots/{botID}/delete", bh.DeleteBot)
		r.Post("/bots/{botID}/name", bh.Rename)
		r.Get("/bots/{botID}/submissions", bh.Submissions)
		r.Get("/bots/{botID}/submissions/{submissionID}", bh.Submission)
		r.Get("/bots/{botID}/activate", bh.Activation)
		r.With(limiter.MiddlewareStrict("bot-activate", 10, time.Minute)).Post("/bots/{botID}/activate", bh.Activate)
		r.Post("/bots/{botID}/publish", bh.Publish)
		r.With(limiter.MiddlewareStrict("bot-activate", 10, time.Minute)).Post("/bots/{botID}/deploy", bh.Deploy)
		r.Post("/bots/{botID}/pause", bh.Pause)
		r.Post("/bots/{botID}/resume", bh.Resume)
		r.Get("/bots/{botID}/draft", bh.Draft)
		r.Post("/bots/{botID}/draft", bh.SaveDraft)
		r.Get("/bots/{botID}/preview", bh.PreviewLanding)
		r.With(limiter.MiddlewareStrict("bot-preview", 20, time.Minute)).Post("/bots/{botID}/preview", bh.StartPreview)
		r.Get("/bots/{botID}/preview/{previewID}", bh.Preview)
		r.Post("/bots/{botID}/preview/{previewID}/choose", bh.ChoosePreview)
		r.Post("/bots/{botID}/preview/{previewID}/restart", bh.RestartPreview)
	})
	ingress := chi.NewRouter()
	ingress.Use(mw.RequestID, mw.Logging, mw.Security)
	ingress.Post("/telegram/bots/{botID}", bh.Webhook)
	ingress.Mount("/", r)
	return ingress
}
