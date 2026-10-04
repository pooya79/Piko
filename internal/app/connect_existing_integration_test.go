package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/bot/telegram"
)

func TestConnectExistingBotRetainsDraftAndPreviewWithoutActivation(t *testing.T) {
	var methods []string
	a, b, _ := botFixture(t, func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		methods = append(methods, method)
		switch method {
		case "getMe":
			fmt.Fprint(w, `{"ok":true,"result":{"id":123456,"is_bot":true,"first_name":"Verified <bot>","username":"verified_bot"}}`)
		case "getWebhookInfo":
			fmt.Fprint(w, `{"ok":true,"result":{"url":"https://elsewhere.test/private-secret","pending_update_count":7}}`)
		default:
			t.Error("connection changed Telegram delivery")
			w.WriteHeader(500)
		}
	})
	if got := b.post("/bots/new", url.Values{"name": {"ایدهٔ من"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.post("/bots/1/draft", draftAtRevision(inquiryDraft(), "1")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	preview := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
	if got := b.post(preview+"/choose", url.Values{"revision": {"1"}, "choice": {"inquiry"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if page := b.send("GET", "/bots/1", nil); !strings.Contains(page.Body.String(), `href="/bots/1/connect"`) {
		t.Fatal("Unconnected Bot has no dedicated connection entry")
	}
	if page := b.send("GET", "/bots/1/connect", nil); page.Code != 200 || !strings.Contains(page.Body.String(), `action="/bots/1/connect"`) || !strings.Contains(page.Body.String(), `type="password"`) {
		t.Fatalf("dedicated credential page: %d", page.Code)
	}
	if got := b.post("/bots/1/connect", url.Values{"token": {" " + testBotToken + " "}}); got.Code != 303 || got.Header().Get("Location") != "/bots/1" {
		t.Fatalf("connection: %d", got.Code)
	}
	if !reflect.DeepEqual(before, renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())) {
		t.Fatal("connection changed the shared Draft or revision")
	}
	page := b.send("GET", "/bots/1", nil).Body.String()
	for _, want := range []string{"Verified &lt;bot&gt;", "verified_bot", "123456", "تأیید شده", "فعال نشده", "۷", "از قبل تنظیم شده", "نسخهٔ منتشرشده: ۰"} {
		if !strings.Contains(page, want) {
			t.Errorf("detail missing %q", want)
		}
	}
	if strings.Contains(page, testBotToken) || strings.Contains(page, "private-secret") {
		t.Fatal("connection exposed credentials or a foreign webhook secret")
	}
	if strings.Join(methods, ",") != "getMe,getWebhookInfo" {
		t.Fatalf("unexpected Telegram work: %v", methods)
	}
	if err := a.db.Close(); err != nil {
		t.Fatal(err)
	}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("restart unexpectedly contacted Telegram")
		w.WriteHeader(500)
	}))
	defer fake.Close()
	restarted, err := newWithTelegram(t.Context(), a.cfg, telegram.NewClient(fake.URL, http.DefaultClient))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.db.Close() })
	b.router = restarted.server.Handler
	if !reflect.DeepEqual(before, renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())) {
		t.Fatal("restart lost the retained Draft")
	}
	if got := b.post(preview+"/choose", url.Values{"revision": {"2"}, "answer": {"پیشرفت محفوظ"}}); got.Code != 303 {
		t.Fatal("connection or restart lost Preview progress")
	}
	if page := b.send("GET", "/bots/1/submissions", nil); strings.Contains(page.Body.String(), "data-submission-id=") {
		t.Fatal("connection made Preview live")
	}
}

func TestConnectExistingDuplicatePreservesBothBotsAndLinksOnlyWithinOwner(t *testing.T) {
	for _, disconnected := range []bool{false, true} {
		t.Run(fmt.Sprintf("disconnected=%t", disconnected), func(t *testing.T) {
			fake := &telegramFake{}
			a, b, _ := botFixture(t, fake.ServeHTTP)
			if got := b.post("/bots/connect", url.Values{"token": {testBotToken}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			if got := b.post("/bots/1/draft", draftAtRevision(welcomeDraft(), "0")); got.Code != 303 {
				t.Fatal(got.Code)
			}
			if disconnected {
				if got := b.post("/bots/1/disconnect", url.Values{}); got.Code != 303 {
					t.Fatal(got.Code)
				}
			}
			if got := b.post("/bots/new", url.Values{"name": {"پیش\u200cنویس دوم"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			before := map[string]string{}
			for _, path := range []string{"/bots/1", "/bots/1/draft", "/bots/2", "/bots/2/draft"} {
				before[path] = b.send("GET", path, nil).Body.String()
			}
			got := b.post("/bots/2/connect", url.Values{"token": {replacementToken}})
			if got.Code != 409 || !strings.Contains(got.Body.String(), "هر دو ربات و پیش\u200cنویس\u200cهایشان حفظ شدند") || !strings.Contains(got.Body.String(), `href="/bots/1"`) || !strings.Contains(got.Body.String(), "باز کردن ربات قبلی") || strings.Contains(got.Body.String(), replacementToken) {
				t.Fatalf("same-owner duplicate feedback: %d", got.Code)
			}
			for path, body := range before {
				if got := b.send("GET", path, nil); got.Body.String() != body {
					t.Fatalf("duplicate changed retained work at %s", path)
				}
			}
			other := newAccountBrowser(t, a.server.Handler)
			other.send("GET", "/register", nil)
			if got := other.post("/register", registerValues("second-owner@example.test", "دیگری", "OwnerPassword123")); got.Code != 303 {
				t.Fatal(got.Code)
			}
			if got := other.post("/bots/new", url.Values{"name": {"ربات خصوصی دیگری"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			got = other.post("/bots/3/connect", url.Values{"token": {replacementToken}})
			if got.Code != 409 {
				t.Fatal(got.Code)
			}
			for _, secret := range []string{`href="/bots/1"`, "Live Bot", "live_bot", "123456", "باز کردن ربات قبلی", replacementToken} {
				if strings.Contains(got.Body.String(), secret) {
					t.Fatalf("cross-owner duplicate exposed %q", secret)
				}
			}
			if page := other.send("GET", "/bots/3", nil); !strings.Contains(page.Body.String(), "هنوز متصل نشده") {
				t.Fatal("duplicate claimed another owner's identity")
			}
			for path, body := range before {
				if b.send("GET", path, nil).Body.String() != body {
					t.Fatal("cross-owner duplicate changed reserved Bot")
				}
			}
		})
	}
}

func TestConnectExistingFailuresPreserveSavedWorkAndClearToken(t *testing.T) {
	for _, tc := range []struct {
		name, method      string
		tokens            []string
		apiStatus, status int
	}{
		{"missing", "", nil, 0, 422},
		{"repeated", "", []string{testBotToken, replacementToken}, 0, 422},
		{"malformed", "", []string{"123:private-malformed"}, 0, 422},
		{"revoked", "getMe", []string{testBotToken}, 401, 422},
		{"verification unavailable", "getMe", []string{testBotToken}, 500, 503},
		{"inspection unavailable", "getWebhookInfo", []string{testBotToken}, 502, 503},
		{"inspection rejected", "getWebhookInfo", []string{testBotToken}, 400, 503},
		{"inspection forbidden", "getWebhookInfo", []string{testBotToken}, 403, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, b, _ := botFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.method == "" {
					t.Error("invalid form reached Telegram")
					w.WriteHeader(500)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/"+tc.method) {
					w.WriteHeader(tc.apiStatus)
					fmt.Fprint(w, "private upstream error "+testBotToken)
					return
				}
				fmt.Fprint(w, `{"ok":true,"result":{"id":123456,"is_bot":true,"first_name":"Unsaved Bot","username":"unsaved_bot"}}`)
			})
			if got := b.post("/bots/new", url.Values{"name": {"کار محفوظ"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			preview := b.post("/bots/1/preview", url.Values{}).Header().Get("Location")
			before := map[string]string{}
			for _, path := range []string{"/bots/1", "/bots/1/draft", preview} {
				before[path] = b.send("GET", path, nil).Body.String()
			}
			got := b.post("/bots/1/connect", url.Values{"token": tc.tokens})
			if got.Code != tc.status || strings.Contains(got.Body.String(), "private upstream error") {
				t.Fatalf("failure feedback: %d", got.Code)
			}
			for _, token := range tc.tokens {
				if strings.Contains(got.Body.String(), token) {
					t.Fatal("failure echoed token")
				}
			}
			for path, body := range before {
				if b.send("GET", path, nil).Body.String() != body {
					t.Fatalf("failed connection changed %s", path)
				}
			}
		})
	}
}

func TestConnectExistingRequiresOwnerPOSTCSRFAndUnconnectedState(t *testing.T) {
	a, b := unconnectedFixture(t)
	b.post("/bots/new", url.Values{"name": {"خصوصی"}})
	guest := newAccountBrowser(t, a.server.Handler)
	guest.send("GET", "/login", nil)
	for _, method := range []string{"GET", "POST"} {
		if got := guest.send(method, "/bots/1/connect", url.Values{"csrf_token": {guest.cookie("piko_csrf")}}); got.Code != 303 || got.Header().Get("Location") != "/login" {
			t.Fatal("guest can access connection")
		}
	}
	other := newAccountBrowser(t, a.server.Handler)
	other.send("GET", "/register", nil)
	other.post("/register", registerValues("other-connect@example.test", "دیگری", "OwnerPassword123"))
	for _, path := range []string{"/bots/1/connect", "/bots/999/connect", "/bots/nope/connect"} {
		if got := other.send("GET", path, nil); got.Code != 404 || strings.Contains(got.Body.String(), "خصوصی") {
			t.Fatal("connection read exposed another owner's Bot")
		}
		if got := other.post(path, url.Values{"token": {testBotToken}}); got.Code != 404 {
			t.Fatal("connection mutation allowed another owner")
		}
	}
	for _, csrf := range []string{"", "wrong"} {
		if got := b.send("POST", "/bots/1/connect", url.Values{"token": {testBotToken}, "csrf_token": {csrf}}); got.Code != 403 {
			t.Fatal("connection accepted invalid CSRF")
		}
	}
	for _, method := range []string{"PUT", "PATCH"} {
		if got := b.send(method, "/bots/1/connect", url.Values{"csrf_token": {b.cookie("piko_csrf")}}); got.Code != 405 {
			t.Fatal("connection accepted non-POST mutation")
		}
	}
	if got := b.send("GET", "/bots/1/connect?token="+testBotToken, nil); got.Code != 200 || strings.Contains(got.Body.String(), testBotToken) {
		t.Fatal("GET consumed or echoed token")
	}
	if page := b.send("GET", "/bots/1", nil); !strings.Contains(page.Body.String(), "هنوز متصل نشده") {
		t.Fatal("rejected requests changed connection state")
	}

	fake := &telegramFake{}
	_, connected, _ := botFixture(t, fake.ServeHTTP)
	connected.post("/bots/connect", url.Values{"token": {testBotToken}})
	for _, action := range []string{"connected", "disconnected"} {
		fake.mu.Lock()
		before := len(fake.calls)
		fake.mu.Unlock()
		for _, method := range []string{"GET", "POST"} {
			if got := connected.send(method, "/bots/1/connect", url.Values{"token": {replacementToken}, "csrf_token": {connected.cookie("piko_csrf")}}); got.Code != 409 {
				t.Fatalf("%s Bot accepted initial connection", action)
			}
		}
		fake.mu.Lock()
		after := len(fake.calls)
		fake.mu.Unlock()
		if after != before {
			t.Fatal("wrong lifecycle state contacted Telegram")
		}
		if action == "connected" {
			connected.post("/bots/1/disconnect", url.Values{})
		}
	}
}

func TestConnectExistingStorageFailureIsAtomicAndPrivate(t *testing.T) {
	fake := &telegramFake{}
	a, b, _ := botFixture(t, fake.ServeHTTP)
	b.post("/bots/new", url.Values{"name": {"کار محفوظ"}})
	before := b.send("GET", "/bots/1", nil).Body.String()
	if _, err := a.db.Exec("CREATE TRIGGER fail_connect BEFORE UPDATE OF telegram_id ON bots BEGIN SELECT RAISE(ABORT, '" + testBotToken + "'); END"); err != nil {
		t.Fatal(err)
	}
	got := b.post("/bots/1/connect", url.Values{"token": {testBotToken}})
	if got.Code != 500 || strings.Contains(got.Body.String(), testBotToken) || !strings.Contains(got.Body.String(), "ذخیرهٔ ربات ممکن نشد") {
		t.Fatalf("storage failure: %d", got.Code)
	}
	if b.send("GET", "/bots/1", nil).Body.String() != before {
		t.Fatal("failed storage partially changed identity or credentials")
	}
	if _, err := a.db.Exec("DROP TRIGGER fail_connect"); err != nil {
		t.Fatal(err)
	}
	if got := b.post("/bots/1/connect", url.Values{"token": {testBotToken}}); got.Code != 303 {
		t.Fatal("failed connection cannot be retried")
	}
}

func TestConnectExistingSlowVerificationRetainsConcurrentDraftEditAndRejectsStaleConnection(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	_, b, _ := botFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			if strings.Contains(r.URL.Path, testBotToken) {
				close(started)
				<-release
			}
			id := 123456
			if strings.Contains(r.URL.Path, replacementToken) {
				id = 654321
			}
			fmt.Fprintf(w, `{"ok":true,"result":{"id":%d,"is_bot":true,"first_name":"Verified Bot","username":"verified_bot"}}`, id)
		} else if strings.HasSuffix(r.URL.Path, "/getWebhookInfo") {
			fmt.Fprint(w, `{"ok":true,"result":{"url":"","pending_update_count":0}}`)
		} else {
			t.Error("connection changed delivery")
			w.WriteHeader(500)
		}
	})
	b.post("/bots/new", url.Values{"name": {"ویرایش هم\u200cزمان"}})
	done := make(chan int, 1)
	go func() { done <- b.post("/bots/1/connect", url.Values{"token": {testBotToken}}).Code }()
	released := false
	defer func() {
		if !released {
			close(release)
			<-done
		}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("verification did not start")
	}
	if got := b.post("/bots/1/draft", draftAtRevision(inquiryDraft(), "1")); got.Code != 303 {
		t.Fatal("verification locked or lost a Draft edit")
	}
	before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	if got := b.post("/bots/1/connect", url.Values{"token": {replacementToken}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	close(release)
	released = true
	select {
	case code := <-done:
		if code != 409 {
			t.Fatalf("stale connection overwrote a verified identity: %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("connection did not finish")
	}
	if page := b.send("GET", "/bots/1", nil); !strings.Contains(page.Body.String(), "654321") || strings.Contains(page.Body.String(), "123456") {
		t.Fatal("stale connection changed identity")
	}
	if !reflect.DeepEqual(before, renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())) {
		t.Fatal("connection discarded a concurrent Draft edit")
	}
}

func TestConnectExistingRetainsOfflinePublicationAndUsesEncryptedTokenAfterRestart(t *testing.T) {
	fake := &telegramFake{}
	a, b, _ := botFixture(t, fake.ServeHTTP)
	b.post("/bots/new", url.Values{"name": {"انتشار آفلاین"}})
	if got := b.post("/bots/1/publish", url.Values{}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if got := b.post("/bots/1/connect", url.Values{"token": {testBotToken}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page := b.send("GET", "/bots/1", nil).Body.String()
	if !strings.Contains(page, "نسخهٔ منتشرشده: ۱") || !strings.Contains(page, "فعال نشده") {
		t.Fatal("connection republished or activated the offline publication")
	}
	// Storage inspection is limited to the credential encryption contract;
	// persistence, publication and lifecycle assertions stay at App HTTP.
	var encrypted []byte
	if err := a.db.QueryRow("SELECT encrypted_token FROM bots WHERE id = 1").Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if len(encrypted) < 28 || strings.Contains(string(encrypted), testBotToken) {
		t.Fatal("token was not encrypted")
	}
	if err := a.db.Close(); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(fake)
	defer api.Close()
	restarted, err := newWithTelegram(t.Context(), a.cfg, telegram.NewClient(api.URL, api.Client()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.db.Close() })
	b.router = restarted.server.Handler
	if got := b.post("/bots/1/activate", url.Values{"operate": {"yes"}}); got.Code != 303 {
		t.Fatalf("saved token could not activate explicitly after restart: %d", got.Code)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	for _, token := range fake.tokens {
		if token != testBotToken {
			t.Fatal("saved token did not decrypt under retained identity")
		}
	}
}

func TestConnectExistingVerificationCannotRecreateDeletedBotOrClaimReservedIdentity(t *testing.T) {
	for _, deleteBot := range []bool{false, true} {
		t.Run(fmt.Sprintf("delete=%t", deleteBot), func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			_, b, _ := botFixture(t, func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/getMe") {
					if strings.Contains(r.URL.Path, testBotToken) {
						close(started)
						<-release
					}
					fmt.Fprint(w, `{"ok":true,"result":{"id":123456,"is_bot":true,"first_name":"Verified Bot","username":"verified_bot"}}`)
				} else if strings.HasSuffix(r.URL.Path, "/getWebhookInfo") {
					fmt.Fprint(w, `{"ok":true,"result":{"url":"","pending_update_count":0}}`)
				} else {
					t.Error("connection changed delivery")
					w.WriteHeader(500)
				}
			})
			b.post("/bots/new", url.Values{"name": {"ربات نخست"}})
			b.post("/bots/new", url.Values{"name": {"ربات دوم"}})
			before := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
			done := make(chan *httptest.ResponseRecorder, 1)
			go func() { done <- b.post("/bots/1/connect", url.Values{"token": {testBotToken}}) }()
			released := false
			defer func() {
				if !released {
					close(release)
					<-done
				}
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("verification did not start")
			}
			if deleteBot {
				if got := b.post("/bots/1/delete", url.Values{"confirm_delete": {"yes"}}); got.Code != 303 {
					t.Fatal(got.Code)
				}
			} else {
				if got := b.post("/bots/2/connect", url.Values{"token": {replacementToken}}); got.Code != 303 {
					t.Fatal(got.Code)
				}
			}
			close(release)
			released = true
			select {
			case got := <-done:
				if got.Code != 409 {
					t.Fatalf("delayed connection: %d", got.Code)
				}
				if !deleteBot && !strings.Contains(got.Body.String(), `href="/bots/2"`) {
					t.Fatal("concurrent duplicate lost owner-authorized link")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("connection did not finish")
			}
			if deleteBot {
				if got := b.send("GET", "/bots/1", nil); got.Code != 404 {
					t.Fatal("connection recreated deleted Bot")
				}
			} else {
				if !reflect.DeepEqual(before, renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())) {
					t.Fatal("concurrent duplicate changed Draft")
				}
				if page := b.send("GET", "/bots/1", nil); !strings.Contains(page.Body.String(), "هنوز متصل نشده") {
					t.Fatal("duplicate merged saved Bots")
				}
			}
		})
	}
}
