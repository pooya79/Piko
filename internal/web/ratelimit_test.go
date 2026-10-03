package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/locale"
	"github.com/pooya79/Piko/internal/platform/database"
	"github.com/pooya79/Piko/internal/testsupport"
)

func TestRateLimitSharedAcrossConnectionsAndExpires(t *testing.T) {
	ctx := context.Background()
	db, path := testsupport.MigratedSQLite(t, ctx)
	other, err := database.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ip := func(*http.Request) string { return "127.0.0.1" }
	var allowed atomic.Int64
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { allowed.Add(1); w.WriteHeader(http.StatusNoContent) })
	handlers := []http.Handler{
		NewRateLimiter(db, log, ip).MiddlewareStrict("mail", 5, time.Hour)(next),
		NewRateLimiter(other, log, ip).MiddlewareStrict("mail", 5, time.Hour)(next),
	}
	var wg sync.WaitGroup
	for i := range 40 {
		wg.Go(func() {
			w := httptest.NewRecorder()
			handlers[i%2].ServeHTTP(w, rateRequest(t))
			if w.Code != http.StatusNoContent && w.Code != http.StatusTooManyRequests {
				t.Errorf("status = %d", w.Code)
			}
		})
	}
	wg.Wait()
	if got := allowed.Load(); got != 5 {
		t.Fatalf("allowed = %d, want 5", got)
	}
	if _, err := other.ExecContext(ctx, "UPDATE rate_limits SET expires_at = 0"); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handlers[0].ServeHTTP(w, rateRequest(t))
	if w.Code != http.StatusNoContent || w.Header().Get("X-RateLimit-Remaining") != "4" {
		t.Fatalf("expired window status = %d, headers = %v", w.Code, w.Header())
	}
}

func TestRateLimiterFailurePolicies(t *testing.T) {
	db, _ := testsupport.MigratedSQLite(t, context.Background())
	_ = db.Close()
	l := NewRateLimiter(db, slog.New(slog.NewTextHandler(io.Discard, nil)), func(*http.Request) string { return "client" })
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for _, tc := range []struct {
		strict bool
		status int
	}{{false, http.StatusNoContent}, {true, http.StatusServiceUnavailable}} {
		middleware := l.Middleware
		if tc.strict {
			middleware = l.MiddlewareStrict
		}
		w := httptest.NewRecorder()
		middleware("mail", 5, time.Minute)(next).ServeHTTP(w, rateRequest(t))
		if w.Code != tc.status {
			t.Errorf("strict = %v, status = %d", tc.strict, w.Code)
		}
	}
}

func rateRequest(t *testing.T) *http.Request {
	t.Helper()
	catalog, err := locale.NewCatalog()
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	return r.WithContext(catalog.With(r.Context()))
}
