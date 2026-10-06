package app

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pooya79/Piko/internal/app/httpapp"
	"github.com/pooya79/Piko/internal/auth"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
	webx "github.com/pooya79/Piko/internal/web"
)

func TestUnsupportedMethodUsesPersianSharedFeedback(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mw := webx.Middleware{Log: logger, Secret: []byte("test-secret"), LocaleCatalog: fixture.TestLocaleCatalog(t)}
	router := httpapp.Router(nil, mw, nil, auth.NewHandler(nil, nil, logger, false), testBotService(t, nil), nil)
	r := httptest.NewRequest(http.MethodGet, "/logout", nil)
	r.AddCookie(&http.Cookie{Name: "piko_language", Value: "en"})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != http.MethodPost {
		t.Fatalf("method rejection status=%d allow=%q", w.Code, w.Header().Get("Allow"))
	}
	for _, want := range []string{`lang="fa"`, `dir="rtl"`, "روش درخواست برای این صفحه مجاز نیست."} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("method rejection missing %q", want)
		}
	}
}
