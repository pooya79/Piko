package web

import (
	"database/sql"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"buildx/internal/platform/database/dbgen"
)

type RateLimiter struct {
	q        *dbgen.Queries
	log      *slog.Logger
	clientIP func(*http.Request) string
}

func NewRateLimiter(db *sql.DB, l *slog.Logger, ip func(*http.Request) string) *RateLimiter {
	return &RateLimiter{q: dbgen.New(db), log: l, clientIP: ip}
}

// Middleware counts requests per bucket and client IP; database failures allow the request through.
func (l *RateLimiter) Middleware(bucket string, limit int, window time.Duration) func(http.Handler) http.Handler {
	return l.middleware(bucket, limit, window, false)
}
func (l *RateLimiter) MiddlewareStrict(bucket string, limit int, window time.Duration) func(http.Handler) http.Handler {
	return l.middleware(bucket, limit, window, true)
}
func (l *RateLimiter) middleware(bucket string, limit int, window time.Duration, failClosed bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := "rate:" + bucket + ":" + l.clientIP(r)
			now := time.Now()
			rate, e := l.q.IncrementRateLimit(r.Context(), dbgen.IncrementRateLimitParams{
				Key: key, ExpiresAt: now.Add(window).UnixMilli(),
			})
			if e != nil {
				l.log.WarnContext(r.Context(), "rate limiter unavailable", "error", e, "bucket", bucket)
				if failClosed {
					RenderError(w, r, http.StatusServiceUnavailable, "Please try again later.")
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			remaining := limit - int(rate.Count)
			if remaining < 0 {
				remaining = 0
			}
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			if rate.Count > int64(limit) {
				w.Header().Set("Retry-After", strconv.Itoa(max(1, int((time.Until(time.UnixMilli(rate.ExpiresAt))+time.Second-1)/time.Second))))
				RenderError(w, r, http.StatusTooManyRequests, "Too many attempts. Try again later.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
