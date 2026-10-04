package app

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/go-chi/chi/v5"
	"github.com/pooya79/Piko/internal/auth"
	"github.com/pooya79/Piko/internal/platform/database/dbgen"
	"github.com/pooya79/Piko/internal/testsupport"
	"github.com/pooya79/Piko/internal/web"
	"github.com/pooya79/Piko/internal/web/request"
	"github.com/pooya79/Piko/internal/web/shell"
	"golang.org/x/net/html"
)

// A test-only feature page supplies shell data through the app's authenticated
// HTTP boundary. Register browser middleware because buildRouter keeps machine
// ingress separate from the browser subrouter.
func workspacePageResponse(t *testing.T, page shell.Page) string {
	t.Helper()
	db, _ := testsupport.MigratedSQLite(t, t.Context())
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	credentials := auth.NewService(dbgen.New(db))
	accounts := auth.NewAccountService(auth.NewAccountRepository(db), credentials)
	mw := web.Middleware{Auth: credentials, LocaleCatalog: testLocaleCatalog(t), Log: log, Secret: []byte("workspace-test-secret")}
	limiter := web.NewRateLimiter(db, log, func(*http.Request) string { return "workspace-test" })
	router := buildRouter(db, mw, limiter, auth.NewHandler(credentials, accounts, log, false), testBotService(t, db), nil).(*chi.Mux)
	router.With(mw.RequestLocale, mw.Session, mw.CSRF, mw.RequireAuth).Get("/test/workspace", func(w http.ResponseWriter, r *http.Request) {
		user, _ := auth.UserFromContext(r.Context())
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := shell.Workspace(user.DisplayName, request.CookieValue(r, auth.CSRFCookie), page, templ.NopComponent).Render(r.Context(), w); err != nil {
			t.Error(err)
		}
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(path string, status int) string {
		t.Helper()
		response, err := client.Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil || response.StatusCode != status {
			t.Fatalf("GET %s: status=%d error=%v", path, response.StatusCode, err)
		}
		return string(body)
	}
	get("/test/workspace", http.StatusSeeOther)
	get("/register", http.StatusOK)
	base, _ := url.Parse(server.URL)
	form := url.Values{"display_name": {"مینا Mina"}, "email": {"workspace@example.test"}, "password": {"workspace-password-123"}}
	for _, cookie := range jar.Cookies(base) {
		if cookie.Name == auth.CSRFCookie {
			form.Set("csrf_token", cookie.Value)
		}
	}
	response, err := client.PostForm(server.URL+"/register", form)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("registration: status=%d", response.StatusCode)
	}
	return get("/test/workspace", http.StatusOK)
}

func TestWorkspacePageIdentityThroughHTTP(t *testing.T) {
	body := workspacePageResponse(t, shell.Page{
		Title: "تنظیمات <ربات>",
		Breadcrumbs: []shell.Breadcrumb{
			{Label: "ربات\u200cها", URL: "/test/bots"},
			{Label: "Mina <bot>"},
		},
	})
	for _, want := range []string{
		"<title>تنظیمات &lt;ربات&gt; · پیکو</title>",
		`href="/test/bots"`, "Mina &lt;bot&gt;", `lang="fa"`, `dir="rtl"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("workspace page missing %q", want)
		}
	}
	if strings.Contains(body, "<bot>") || strings.Contains(body, "<ربات>") {
		t.Error("page identity must escape supplied labels")
	}
}

func TestWorkspaceBotNavigationThroughHTTP(t *testing.T) {
	body := workspacePageResponse(t, shell.Page{
		Title: "ربات\u200cها", ActiveNav: shell.BotsNav, BotsURL: "/test/bots",
		RecentBots: []shell.BotLink{{Name: "Mina <bot>", URL: "/test/bots/42"}},
	})
	doc, err := html.Parse(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var dashboard, bots, recent *html.Node
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Data == "a" {
			switch feedbackAttr(n, "href") {
			case "/dashboard":
				if strings.Contains(feedbackAttr(n, "class"), "piko-nav-item") {
					dashboard = n
				}
			case "/test/bots":
				bots = n
			case "/test/bots/42":
				recent = n
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(doc)
	if dashboard == nil || feedbackAttr(dashboard, "aria-current") != "" || strings.Contains(feedbackAttr(dashboard, "class"), "piko-nav-active") {
		t.Error("Dashboard must remain reachable without being selected on a Bot page")
	}
	if bots == nil || feedbackAttr(bots, "aria-current") != "page" || !strings.Contains(feedbackAttr(bots, "class"), "piko-nav-active") {
		t.Error("Bot navigation must identify the current section")
	}
	if recent == nil || !strings.Contains(body, "Mina &lt;bot&gt;") || strings.Contains(body, "هنوز رباتی نساخته\u200cای") {
		t.Error("supplied Bot navigation must replace the empty state with escaped real names")
	}
	for _, unavailable := range []string{`href="/chat"`, `href="/analytics"`, `href="/billing"`, "بدون مشکل", "٪"} {
		if strings.Contains(body, unavailable) {
			t.Errorf("shell enabled an unavailable feature or invented Bot data: %s", unavailable)
		}
	}
}

func TestWorkspaceDefaultsThroughHTTP(t *testing.T) {
	for _, tc := range []struct {
		name  string
		page  shell.Page
		title string
	}{
		{name: "Dashboard defaults", title: "داشبورد"},
		{name: "page title supplies current breadcrumb", page: shell.Page{Title: "تنظیمات ربات"}, title: "تنظیمات ربات"},
		{name: "unavailable Bot destination", page: shell.Page{ActiveNav: shell.BotsNav, RecentBots: []shell.BotLink{{Name: "ربات بدون صفحه"}}}, title: "داشبورد"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := workspacePageResponse(t, tc.page)
			for _, want := range []string{
				"<title>" + tc.title + " · پیکو</title>",
				`<strong aria-current="page"><bdi dir="auto">` + tc.title,
				`href="/account"`, `action="/logout"`, `method="post"`,
				`name="csrf_token"`, `data-theme-choice="system"`,
			} {
				if !strings.Contains(body, want) {
					t.Errorf("workspace missing %q", want)
				}
			}
			if strings.Contains(body, `href="/bots"`) || strings.Contains(body, `href=""`) {
				t.Error("unimplemented destinations must not become links")
			}
		})
	}
}
