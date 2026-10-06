package app

import (
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/pooya79/Piko/internal/app/httpapp"
	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
	"github.com/pooya79/Piko/internal/testsupport"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
	"github.com/pooya79/Piko/internal/web"
)

func accountTestRouter(t *testing.T, db *sql.DB) (http.Handler, *auth.Service) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	credentials := auth.NewService(dbgen.New(db))
	accounts := auth.NewAccountService(auth.NewAccountRepository(db), credentials)
	mw := web.Middleware{Auth: credentials, LocaleCatalog: fixture.TestLocaleCatalog(t), Log: logger, Secret: []byte("test-secret")}
	return httpapp.Router(db, mw, web.NewRateLimiter(db, logger, mw.ClientIP), auth.NewHandler(credentials, accounts, logger, false), testBotService(t, db), nil), credentials
}

func TestRemovedAccountRoutesAreNotFoundWithValidCSRF(t *testing.T) {
	db, _ := testsupport.MigratedSQLite(t, t.Context())
	router, _ := accountTestRouter(t, db)
	visitor := fixture.NewAccountBrowser(t, router)
	visitor.Send(http.MethodGet, "/login", nil)
	for _, path := range []string{"/language", "/verify", "/verify/pending", "/verify/resend", "/verify/cancel", "/password/forgot", "/password/reset"} {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			var got *httptest.ResponseRecorder
			if method == http.MethodPost {
				got = visitor.Post(path, url.Values{})
			} else {
				got = visitor.Send(method, path, nil)
			}
			if got.Code != http.StatusNotFound {
				t.Errorf("%s %s status=%d", method, path, got.Code)
			}
		}
	}
	for _, path := range []string{"/login", "/register", "/account"} {
		got := visitor.Send(http.MethodGet, path, nil)
		if strings.Contains(got.Body.String(), "/verify") || strings.Contains(got.Body.String(), "/password/") {
			t.Errorf("%s retains retired links", path)
		}
	}
}
