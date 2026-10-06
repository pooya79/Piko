package builder_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pooya79/Piko/internal/builder"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestStudioEntryOpensPikoWithoutCreatingBotOrChat(t *testing.T) {
	_, b := fixture.UnconnectedFixture(t)
	for _, path := range []string{"/builder"} {
		got := b.Send("GET", path, nil)
		body := got.Body.String()
		if got.Code != 200 || !strings.Contains(body, `data-piko-studio`) || !strings.Contains(body, `id="builder-message"`) {
			t.Fatalf("%s does not open the conversation: %d", path, got.Code)
		}
		for _, template := range []string{"inquiry", "registration", "booking"} {
			if !strings.Contains(body, `data-example="`+template+`"`) {
				t.Fatalf("missing editable %s example", template)
			}
		}
		for _, field := range []string{"name", "title", "token"} {
			if strings.Contains(body, `name="`+field+`"`) {
				t.Fatalf("entry requires %s", field)
			}
		}
	}
	if got := b.Send("GET", "/chats/1", nil); got.Code != 404 {
		t.Fatal("reading the welcome screen created a chat")
	}
	if got := b.Send("GET", "/bots/1", nil); got.Code != 404 {
		t.Fatal("reading the welcome screen created a Bot")
	}
	if got := b.Post("/chats", url.Values{}); got.Code != 303 || got.Header().Get("Location") != "/chats/1" {
		t.Fatal("explicit conversation creation unavailable")
	}
}

func TestStudioFirstTurnValidationAndCSRFSaveNoConversation(t *testing.T) {
	_, b := fixture.UnconnectedFixture(t)
	for _, messages := range [][]string{{" "}, {"one", "two"}, {strings.Repeat("س", 32769)}} {
		if got := b.Post("/chats", url.Values{"message": messages}); got.Code != 422 {
			t.Fatalf("invalid first message admitted: %d", got.Code)
		}
	}
	if got := b.Send("POST", "/chats", url.Values{"message": {"پرسش"}, "csrf_token": {"wrong"}}); got.Code != 403 {
		t.Fatal("welcome composer bypassed CSRF")
	}
	if got := b.Send("GET", "/chats/1", nil); got.Code != 404 {
		t.Fatal("rejected first turn created a saved conversation")
	}
}

func TestStudioFragmentFollowsCommittedAssociationWithoutReplayingWork(t *testing.T) {
	var calls atomic.Int64
	_, b := fixture.GeneralBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			fixture.MemoryReply(w, `{"intent":"build"}`)
		case 2:
			fixture.BuilderToolReply(w, "prepare_bot", map[string]string{"name": "ربات فضای ساخت", "definition": fixture.BuilderFormDraft})
		default:
			fixture.MemoryReply(w, "پیش\u200cنویس ساخته شد")
		}
	})
	path := fixture.StartPikoChat(t, b)
	b.Post(path+"/messages", url.Values{"message": {"یک ربات ثبت نام بساز"}})
	fixture.WaitBuilder(t, b, path, "succeeded")
	fragment := func(browser *fixture.Browser) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("X-Piko-Studio", "fragment")
		for _, cookie := range browser.Jar.Cookies(browser.Base) {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		browser.Router.ServeHTTP(w, r)
		return w
	}
	for range 2 {
		got := fragment(b)
		body := got.Body.String()
		if got.Code != 200 || strings.Contains(body, "<html") || strings.Contains(body, "<script") || !strings.Contains(body, `data-chat-url="/bots/1/chats/1"`) || !strings.Contains(body, "ربات فضای ساخت") || !strings.Contains(body, `data-draft-revision="1"`) {
			t.Fatal("fragment did not render the committed Bot association")
		}
		if got.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("private fragment is cacheable")
		}
	}
	if calls.Load() != 3 || b.Send("GET", "/bots/2", nil).Code != 404 {
		t.Fatal("fragment read replayed work")
	}
	other := fixture.NewAccountBrowser(t, b.Router)
	other.Send("GET", "/register", nil)
	other.Post("/register", fixture.RegisterValues("fragment-other@example.test", "دیگری", "OwnerPassword123"))
	if got := fragment(other); got.Code != 404 || strings.Contains(got.Body.String(), "ربات فضای ساخت") {
		t.Fatal("private studio fragment leaked")
	}
}

func TestStudioWelcomeComposerSendsFirstMessageInSavedChat(t *testing.T) {
	_, b := fixture.GeneralBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
		fixture.MemoryReply(w, "پاسخ نخست پیکو")
	})
	got := b.Post("/chats", url.Values{"message": {"پیکو چه امکاناتی دارد؟"}, "request_key": {"first-question"}})
	if got.Code != 303 {
		t.Fatal(got.Code)
	}
	page := fixture.WaitBuilder(t, b, got.Header().Get("Location"), "succeeded")
	if !strings.Contains(page, "پیکو چه امکاناتی دارد؟") || !strings.Contains(page, "پاسخ نخست پیکو") {
		t.Fatal("welcome composer did not send and save the first turn")
	}
	if got := b.Send("GET", "/bots/1", nil); got.Code != 404 {
		t.Fatal("product question created a Bot")
	}
}

func TestStudioFirstSubmissionReplayKeepsOneChatRunAndBot(t *testing.T) {
	var calls atomic.Int64
	a, b := fixture.GeneralBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			fixture.MemoryReply(w, `{"intent":"build"}`)
		case 2:
			fixture.BuilderToolReply(w, "prepare_bot", map[string]string{"name": "ربات نخست", "definition": fixture.BuilderFormDraft})
		default:
			fixture.MemoryReply(w, "ربات ساخته شد")
		}
	})
	values := url.Values{"message": {"یک ربات ثبت نام بساز"}, "request_key": {"welcome-replay"}}
	first := b.Post("/chats", values)
	if first.Code != 303 {
		t.Fatal(first.Code)
	}
	path := first.Header().Get("Location")
	second := b.Post("/chats", values)
	if second.Code != 303 || second.Header().Get("Location") != path {
		t.Fatal("welcome replay created another conversation")
	}
	fixture.WaitBuilder(t, b, path, "succeeded")
	a.StopWork()
	a.Builder.Wait()
	_ = a.DB.Close()
	restarted, err := fixture.New(t.Context(), a.Config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.StopWork(); restarted.Builder.Wait(); _ = restarted.DB.Close() })
	b.Router = restarted.Handler
	if got := b.Post("/chats", values); got.Code != 303 || got.Header().Get("Location") != "/bots/1/chats/1" {
		t.Fatal("restart and lost initial success response cannot resolve the same Bot chat")
	}
	if calls.Load() != 3 || b.Send("GET", "/bots/2", nil).Code != 404 || b.Send("GET", "/chats/2", nil).Code != 404 {
		t.Fatal("first submission replay duplicated work")
	}
	values.Set("message", "پیام متفاوت")
	if got := b.Post("/chats", values); got.Code != 422 {
		t.Fatal("welcome key accepted a different message")
	}
}

func TestStudioSavedSelectionShowsOwnedGeneralAndBotChats(t *testing.T) {
	_, b := fixture.UnconnectedFixture(t)
	general := fixture.StartPikoChat(t, b)
	b.Post("/bots/new", url.Values{"name": {"کافه لیمو"}})
	attached := b.Post("/bots/1/chats", url.Values{"title": {"منوی کافه"}}).Header().Get("Location")
	for _, path := range []string{"/builder", general, attached} {
		got := b.Send("GET", path, nil)
		body := got.Body.String()
		if got.Code != 200 || !strings.Contains(body, `href="`+general+`"`) || !strings.Contains(body, `href="`+attached+`"`) || !strings.Contains(body, "کافه لیمو") || !strings.Contains(body, "گفت\u200cوگوی عمومی") {
			t.Fatalf("saved chat selection or association missing at %s", path)
		}
	}
	other := fixture.NewAccountBrowser(t, b.Router)
	other.Send("GET", "/register", nil)
	other.Post("/register", fixture.RegisterValues("studio-other@example.test", "دیگری", "OwnerPassword123"))
	if got := other.Send("GET", "/builder", nil); strings.Contains(got.Body.String(), "منوی کافه") || strings.Contains(got.Body.String(), "کافه لیمو") {
		t.Fatal("saved chat selection leaked another account")
	}
	if got := other.Send("GET", attached, nil); got.Code != 404 {
		t.Fatal("saved selection bypassed ownership")
	}
}
