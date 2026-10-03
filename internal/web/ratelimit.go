package web

import (
	"github.com/redis/go-redis/v9"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

type RateLimiter struct {
	redis    *redis.Client
	log      *slog.Logger
	clientIP func(*http.Request) string
}

func NewRateLimiter(r *redis.Client, l *slog.Logger, ip func(*http.Request) string) *RateLimiter {
	return &RateLimiter{redis: r, log: l, clientIP: ip}
}

// Middleware counts requests per bucket and client IP; Redis failures allow the request through.
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
			pipe := l.redis.TxPipeline()
			count := pipe.Incr(r.Context(), key)
			pipe.ExpireNX(r.Context(), key, window)
			if _, e := pipe.Exec(r.Context()); e != nil {
				l.log.WarnContext(r.Context(), "rate limiter unavailable", "error", e, "bucket", bucket)
				if failClosed {
					RenderError(w, r, http.StatusServiceUnavailable, "Please try again later.")
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			remaining := limit - int(count.Val())
			if remaining < 0 {
				remaining = 0
			}
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
			if count.Val() > int64(limit) {
				w.Header().Set("Retry-After", strconv.Itoa(int(window.Seconds())))
				RenderError(w, r, http.StatusTooManyRequests, "Too many attempts. Try again later.")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
