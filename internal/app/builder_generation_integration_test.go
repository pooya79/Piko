package app

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/builder"
	"github.com/pooya79/Piko/internal/platform/database"
	"github.com/pooya79/Piko/internal/testsupport"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func builderFixture(t *testing.T, config builder.Config, provider http.HandlerFunc) (*App, *fixture.Browser) {
	t.Helper()
	return builderFixtureWithLogLevel(t, config, "error", provider)
}

func builderFixtureWithLogLevel(t *testing.T, config builder.Config, logLevel string, provider http.HandlerFunc) (*App, *fixture.Browser) {
	t.Helper()
	a, b := generalBuilderFixture(t, config, logLevel, provider)
	if got := b.Post("/bots/new", url.Values{"name": {"ربات گفتگو"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, title := range []string{"گفتگوی اول", "گفتگوی دوم"} {
		fixture.SeedBotChat(t, a.db, 1, title)
	}
	return a, b
}

func generalBuilderFixture(t *testing.T, config builder.Config, logLevel string, provider http.HandlerFunc) (*App, *fixture.Browser) {
	t.Helper()
	_, path := testsupport.MigratedSQLite(t, t.Context())
	fake := httptest.NewServer(fixture.StreamingBuilderProvider(provider))
	t.Cleanup(fake.Close)
	config.APIKey, config.BaseURL = "test-server-key", fake.URL+"/v1"
	cfg := Config{DatabasePath: path, HTTPAddr: "127.0.0.1:0", SessionSecret: "builder-test-secret-at-least-32-characters", BotEncryptionKey: "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=", LogLevel: logLevel, ShutdownPeriod: time.Second, Builder: config}
	a, err := New(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.stopRequests(); a.builder.Wait(); _ = a.db.Close() })
	b := fixture.NewAccountBrowser(t, a.server.Handler)
	b.Send("GET", "/register", nil)
	if got := b.Post("/register", fixture.RegisterValues("builder-owner@example.test", "مینا", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	return a, b
}

func TestBuilderShutdownCancelsAndJoinsProviderBeforeDatabaseClose(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
		close(cancelled)
	})
	stop := startBuilderApp(t, a)
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"باقی می ماند"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not start")
	}
	stop()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("provider not cancelled")
	}
	restarted, err := New(context.Background(), a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.stopRequests(); restarted.builder.Wait(); _ = restarted.db.Close() })
	b.Router = restarted.server.Handler
	page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "interrupted")
	if !strings.Contains(page, "باقی می ماند") || !strings.Contains(page, `data-admitted="1"`) {
		t.Fatal("shutdown lost admitted request")
	}
}

func startBuilderApp(t *testing.T, a *App) func() {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	a.server.Addr = listener.Addr().String()
	_ = listener.Close()
	stop := runDeliveryApp(t, a)
	deadline := time.Now().Add(3 * time.Second)
	for {
		response, err := http.Get("http://" + a.server.Addr + "/health/live")
		if err == nil {
			_ = response.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server did not start")
		}
		time.Sleep(time.Millisecond)
	}
	return stop
}

func TestBuilderStartingAnotherAppPreservesLiveRunLease(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"نتیجه محفوظ"},"finish_reason":"stop"}]}`))
	})
	defer close(release)
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"ادامه"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("not started")
	}
	second, err := New(t.Context(), a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	startBuilderApp(t, second)
	other := fixture.NewAccountBrowser(t, second.server.Handler)
	other.Jar.SetCookies(other.Base, b.Jar.Cookies(b.Base))
	if page := other.Send("GET", "/bots/1/chats/1", nil); !strings.Contains(page.Body.String(), `data-run-status="running"`) {
		t.Fatal("startup interrupted live run")
	}
	if got := other.Post("/bots/1/chats/2/messages", url.Values{"message": {"رقابت"}}); got.Code != 409 {
		t.Fatal("startup released live Bot")
	}
	release <- struct{}{}
	fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
}

func TestBuilderRecoversAbandonedRunWithoutRepeatingProviderCall(t *testing.T) {
	var calls atomic.Int64
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) })
	// A crashed process left committed admission and owner history, with no live lease.
	if _, err := a.db.Exec(`INSERT INTO builder_runs(owner_id,bot_id,chat_id,day,model,draft_revision,status,created_at,lease_until) VALUES(1,1,1,?,'openai/gpt-6-luna',1,'running',1,0)`, time.Now().UTC().Format("2006-01-02")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO builder_messages(chat_id,sequence,role,content,created_at) VALUES(1,1,'owner','درخواست پیش از توقف',1)`); err != nil {
		t.Fatal(err)
	}
	a.stopRequests()
	a.builder.Wait()
	_ = a.db.Close()
	restarted, err := New(t.Context(), a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	startBuilderApp(t, restarted)
	b.Router = restarted.server.Handler
	page := fixture.WaitBuilder(t, b, "/bots/1/chats/1", "interrupted")
	if !strings.Contains(page, "درخواست پیش از توقف") || !strings.Contains(page, `data-admitted="1"`) || calls.Load() != 0 {
		t.Fatal("abandoned request lost or replayed")
	}
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"تلاش صریح"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	fixture.WaitBuilder(t, b, "/bots/1/chats/1", "failed")
	if calls.Load() != 1 {
		t.Fatal("explicit retry did not release Bot")
	}
}

func TestBuilderShutdownBoundsAccountingUnderSQLiteContention(t *testing.T) {
	started, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"reply"},"finish_reason":"stop"}],"usage":{"total_tokens":8}}`))
		close(returned)
	})
	stop := startBuilderApp(t, a)
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"request"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	<-started
	blocker, err := database.Open(t.Context(), a.cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	tx, err := blocker.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	<-returned
	time.Sleep(50 * time.Millisecond)
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
		_ = tx.Rollback()
	case <-time.After(a.cfg.ShutdownPeriod + 500*time.Millisecond):
		_ = tx.Rollback()
		<-done
		t.Error("Builder shutdown exceeded configured one-second ShutdownPeriod while accounting waited on SQLite")
	}
}

func TestBuilderShutdownBoundsAdmissionUnderSQLiteContention(t *testing.T) {
	a, b := builderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) })
	stop := startBuilderApp(t, a)
	blocker, err := database.Open(t.Context(), a.cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	tx, err := blocker.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	requestDone := make(chan int, 1)
	go func() { requestDone <- b.Post("/bots/1/chats/1/messages", url.Values{"message": {"request"}}).Code }()
	time.Sleep(100 * time.Millisecond)
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
		_ = tx.Rollback()
	case <-time.After(a.cfg.ShutdownPeriod + 500*time.Millisecond):
		_ = tx.Rollback()
		<-done
		t.Error("Builder shutdown exceeded configured one-second ShutdownPeriod while admission waited on SQLite")
	}
	<-requestDone
}
