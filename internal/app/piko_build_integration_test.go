package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"encoding/json"
	"github.com/pooya79/Piko/internal/bot/templates/booking"
	"github.com/pooya79/Piko/internal/bot/templates/inquiry"
	"github.com/pooya79/Piko/internal/bot/templates/registration"
	"github.com/pooya79/Piko/internal/builder"
)

func TestPikoBuildCreatesBotInOriginalChatOnce(t *testing.T) {
	var calls atomic.Int64
	_, b := generalBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			memoryReply(w, `{"intent":"build"}`)
		case 2:
			builderToolReply(w, "prepare_bot", map[string]string{"name": "ثبت نام کلاس", "definition": builderFormDraft})
		default:
			memoryReply(w, "طرح ثبت نام آماده شد؛ پیش نمایش را امتحان کنید.")
		}
	})
	path := startPikoChat(t, b)
	values := url.Values{"message": {"یک ربات ثبت نام کلاس بساز"}, "request_key": {"initial-build-1"}}
	if got := b.post(path+"/messages", values); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page := waitBuilder(t, b, path, "succeeded")
	if !strings.Contains(page, `href="/bots/1/draft"`) || !strings.Contains(page, "ثبت نام کلاس") || !strings.Contains(page, values.Get("message")) {
		t.Fatal("original chat did not become Bot workspace", page)
	}
	if pane := changesPane(t, page); !strings.Contains(pane, `data-change-result="created"`) || !strings.Contains(pane, `data-change-action="added"`) || strings.Contains(pane, "/undo") {
		t.Fatal("initial committed Draft missing truthful Changes or offers Undo without a prior Draft")
	}
	if got := b.send("GET", "/bots/1/draft", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "ثبت نام") {
		t.Fatal("initial Draft missing")
	}
	for range 2 {
		if got := b.post(path+"/messages", values); got.Code != 303 {
			t.Fatal("duplicate", got.Code)
		}
		if got := b.send("GET", path+"/status", nil); got.Code != 200 {
			t.Fatal(got.Code)
		}
	}
	if calls.Load() != 3 || b.send("GET", "/bots/2", nil).Code != 404 {
		t.Fatal("lost-response submission replayed creation")
	}
}

func TestPikoInitialBuildFailuresRetainConversationWithoutBot(t *testing.T) {
	for _, fault := range []string{"provider", "tool", "missing-stage", "invalid", "name", "truncated", "budget", "bot-storage", "draft-storage", "association-storage", "reply-storage", "timeout"} {
		t.Run(fault, func(t *testing.T) {
			var calls atomic.Int64
			config := builder.Config{}
			if fault == "budget" {
				config.MaxCalls = 2
			}
			if fault == "timeout" {
				config.RunTimeout = 200 * time.Millisecond
			}
			a, b := generalBuilderFixture(t, config, "error", func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				switch calls.Add(1) {
				case 1:
					memoryReply(w, `{"intent":"build"}`)
				case 2:
					if fault == "missing-stage" {
						memoryReply(w, "پاسخ موفق ذخیره نشده")
						return
					}
					definition, name := builderFormDraft, "ربات کلاس"
					if fault == "invalid" {
						definition = `{"version":99}`
					}
					if fault == "name" {
						name = strings.Repeat("س", 81)
					}
					builderToolReply(w, "prepare_bot", map[string]string{"name": name, "definition": definition})
				default:
					switch fault {
					case "provider":
						w.WriteHeader(500)
					case "tool":
						builderToolReply(w, "unsupported_tool", map[string]string{})
					case "truncated":
						_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ناقص"},"finish_reason":"length"}]}`))
					case "timeout":
						<-r.Context().Done()
					default:
						memoryReply(w, "پاسخ موفق ذخیره نشده")
					}
				}
			})
			triggers := map[string]string{
				"bot-storage":         `CREATE TRIGGER fail_initial BEFORE INSERT ON bots BEGIN SELECT RAISE(ABORT,'private storage failure'); END`,
				"draft-storage":       `CREATE TRIGGER fail_initial BEFORE INSERT ON bot_drafts BEGIN SELECT RAISE(ABORT,'private storage failure'); END`,
				"association-storage": `CREATE TRIGGER fail_initial BEFORE UPDATE OF bot_id ON builder_chats BEGIN SELECT RAISE(ABORT,'private storage failure'); END`,
				"reply-storage":       `CREATE TRIGGER fail_initial BEFORE INSERT ON builder_messages WHEN NEW.role='model' BEGIN SELECT RAISE(ABORT,'private storage failure'); END`,
			}
			if trigger := triggers[fault]; trigger != "" {
				if _, err := a.db.Exec(trigger); err != nil {
					t.Fatal(err)
				}
			}
			path := startPikoChat(t, b)
			values := url.Values{"message": {"ربات کلاس بساز"}, "request_key": {"failure-request"}}
			if got := b.post(path+"/messages", values); got.Code != 303 {
				t.Fatal(got.Code)
			}
			status := "failed"
			if fault == "timeout" {
				status = "timeout"
			}
			page := waitBuilder(t, b, path, status)
			if !strings.Contains(page, values.Get("message")) || strings.Contains(page, "پاسخ موفق ذخیره نشده") || strings.Contains(page, "private storage failure") || strings.Contains(page, `data-draft-revision=`) {
				t.Fatal("failure lost conversation or leaked partial success")
			}
			if strings.Contains(page, `data-change-action=`) || strings.Contains(page, "/undo") {
				t.Fatal("failed initial creation displays saved Changes or Undo")
			}
			if got := b.send("GET", "/bots/1", nil); got.Code != 404 {
				t.Fatal("failure left a Bot")
			}
			before := calls.Load()
			if got := b.post(path+"/messages", values); got.Code != 303 {
				t.Fatal(got.Code)
			}
			if calls.Load() != before {
				t.Fatal("replay automatically retried failure")
			}
		})
	}
}

func TestPikoInitialBuildStopAndRestartFenceStagedCreation(t *testing.T) {
	for _, ending := range []string{"stop", "restart"} {
		t.Run(ending, func(t *testing.T) {
			staged := make(chan struct{})
			var calls atomic.Int64
			a, b := generalBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				switch calls.Add(1) {
				case 1:
					memoryReply(w, `{"intent":"build"}`)
				case 2:
					builderToolReply(w, "prepare_bot", map[string]string{"name": "کلاس", "definition": builderFormDraft})
				default:
					close(staged)
					<-r.Context().Done()
				}
			})
			path := startPikoChat(t, b)
			values := url.Values{"message": {"ربات بساز"}, "request_key": {"staged-request"}}
			if got := b.post(path+"/messages", values); got.Code != 303 {
				t.Fatal(got.Code)
			}
			select {
			case <-staged:
			case <-time.After(3 * time.Second):
				t.Fatal("candidate not staged")
			}
			status := "stopped"
			if ending == "stop" {
				if got := b.post(path+"/runs/1/stop", url.Values{}); got.Code != 303 {
					t.Fatal(got.Code)
				}
			} else {
				a.stopRequests()
				a.builder.Wait()
				_ = a.db.Close()
				restarted, err := New(context.Background(), a.cfg)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { restarted.stopRequests(); restarted.builder.Wait(); _ = restarted.db.Close() })
				b.router = restarted.server.Handler
				status = "interrupted"
			}
			page := waitBuilder(t, b, path, status)
			if !strings.Contains(page, values.Get("message")) || b.send("GET", "/bots/1", nil).Code != 404 {
				t.Fatal("cancellation persisted staged Bot or lost history")
			}
			server := httptest.NewServer(b.router)
			defer server.Close()
			for range 2 {
				req, _ := http.NewRequestWithContext(t.Context(), "GET", server.URL+path+"/stream", nil)
				for _, cookie := range b.jar.Cookies(b.base) {
					req.AddCookie(cookie)
				}
				res, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, res.Body)
				_ = res.Body.Close()
				if res.StatusCode != 200 {
					t.Fatal(res.StatusCode)
				}
			}
			if got := b.post(path+"/messages", values); got.Code != 303 || calls.Load() != 3 {
				t.Fatal("reconnect or duplicate replayed staged creation")
			}
		})
	}
}

func TestPikoBuildStreamObservesConversionWithoutReadmission(t *testing.T) {
	staged, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	_, b := generalBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		switch calls.Add(1) {
		case 1:
			memoryReply(w, `{"intent":"build"}`)
		case 2:
			builderToolReply(w, "prepare_bot", map[string]string{"name": "کلاس", "definition": builderFormDraft})
		default:
			close(staged)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			memoryReply(w, "طرح آماده شد")
		}
	})
	defer close(release)
	path := startPikoChat(t, b)
	values := url.Values{"message": {"ربات کلاس بساز"}, "request_key": {"stream-build"}}
	if got := b.post(path+"/messages", values); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-staged:
	case <-time.After(3 * time.Second):
		t.Fatal("not staged")
	}
	if got := b.post(path+"/messages", values); got.Code != 303 {
		t.Fatal("duplicate active request", got.Code)
	}
	server := httptest.NewServer(b.router)
	defer server.Close()
	req, _ := http.NewRequestWithContext(t.Context(), "GET", server.URL+path+"/stream", nil)
	for _, cookie := range b.jar.Cookies(b.base) {
		req.AddCookie(cookie)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	release <- struct{}{}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != 200 || !strings.Contains(string(body), `"succeeded"`) || strings.Contains(string(body), "intent") || calls.Load() != 3 {
		t.Fatal("stream lost conversion or replayed generation", string(body))
	}
}

func TestPikoConversionKeepsPrivateMemorySharedDraftAndDeletionScope(t *testing.T) {
	var calls atomic.Int64
	requests := make(chan string, 32)
	a, b := generalBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- string(body)
		if strings.Contains(string(body), "Summarize older messages") {
			memoryReply(w, "حافظه خصوصی کلاس")
			return
		}
		switch calls.Add(1) {
		case 1, 2, 3, 4, 5, 6, 7:
			memoryReply(w, "پاسخ عمومی")
		case 8:
			memoryReply(w, `{"intent":"build"}`)
		case 9:
			builderToolReply(w, "prepare_bot", map[string]string{"name": "کلاس زبان", "definition": builderFormDraft})
		case 10:
			memoryReply(w, "طرح کلاس آماده شد")
		case 11:
			builderToolReply(w, "read_draft", map[string]string{})
		case 12:
			builderToolReply(w, "prepare_draft", map[string]string{"definition": strings.Replace(builderFormDraft, "سلام", "سلام تازه", 1)})
		default:
			memoryReply(w, "پاسخ ادامه کلاس")
		}
	})
	path, unrelated := startPikoChat(t, b), startPikoChat(t, b)
	for turn := range 7 {
		if got := b.post(path+"/messages", url.Values{"message": {fmt.Sprintf("پرسش خصوصی کلاس %d", turn)}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		waitBuilder(t, b, path, "succeeded")
	}
	for len(requests) > 0 {
		<-requests
	}
	values := url.Values{"message": {"حالا ربات کلاس بساز"}, "request_key": {"memory-build"}}
	if got := b.post(path+"/messages", values); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page := waitBuilder(t, b, path, "succeeded")
	if !strings.Contains(page, "پرسش خصوصی کلاس 0") || !strings.Contains(page, `data-run-result="created"`) {
		t.Fatal("conversion lost earlier conversation")
	}
	for len(requests) > 0 {
		if request := <-requests; !strings.Contains(request, "Summarize older messages") && !strings.Contains(request, "حافظه خصوصی کلاس") {
			t.Fatal("conversion lost private summary")
		}
	}
	other := newAccountBrowser(t, b.router)
	other.send("GET", "/register", nil)
	if got := other.post("/register", registerValues("other-build@example.test", "دیگری", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, urlPath := range []string{path, path + "/status", "/bots/1/chats/1", "/bots/1/draft"} {
		if got := other.send("GET", urlPath, nil); got.Code != 404 {
			t.Fatal("cross-owner read", urlPath, got.Code)
		}
	}
	for _, urlPath := range []string{path + "/messages", "/bots/1/name"} {
		if got := other.post(urlPath, url.Values{"message": {"بساز"}, "request_key": {"memory-build"}, "name": {"نام دیگری"}}); got.Code != 404 {
			t.Fatal("cross-owner transition", got.Code)
		}
	}
	if got := b.send("GET", "/bots/1/name", nil); got.Code != 405 {
		t.Fatal("rename accepts GET")
	}
	if got := b.send("POST", "/bots/1/name", url.Values{"name": {"نام تازه"}, "csrf_token": {"wrong"}}); got.Code != 403 {
		t.Fatal("rename lacks CSRF")
	}
	for _, name := range []string{"", strings.Repeat("س", 81), "bad\nname"} {
		if got := b.post("/bots/1/name", url.Values{"name": {name}}); got.Code != 422 {
			t.Fatal("invalid name accepted", got.Code)
		}
	}
	if got := b.post("/bots/1/name", url.Values{"name": {"نام تازه"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if page := b.send("GET", path, nil); !strings.Contains(page.Body.String(), "نام تازه") {
		t.Fatal("workspace name not editable")
	}
	// Saved creation, deduplication and original links survive a process restart.
	a.stopRequests()
	a.builder.Wait()
	_ = a.db.Close()
	restarted, err := New(t.Context(), a.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.stopRequests(); restarted.builder.Wait(); _ = restarted.db.Close() })
	b.router = restarted.server.Handler
	if got := b.post(path+"/messages", values); got.Code != 303 || calls.Load() != 10 {
		t.Fatal("restart replayed committed creation")
	}
	if got := b.post(path+"/messages", url.Values{"message": {"پیام خوش آمد تازه کن"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page = waitBuilder(t, b, path, "succeeded")
	if !strings.Contains(page, `data-run-result="saved"`) || !strings.Contains(page, `data-draft-revision="2"`) {
		t.Fatal("converted chat cannot edit shared Draft")
	}
	if got := b.post("/bots/1/preview", url.Values{}); got.Code != 303 || b.send("GET", got.Header().Get("Location"), nil).Code != 200 {
		t.Fatal("converted Draft cannot Preview")
	}
	second := b.post("/bots/1/chats", url.Values{"title": {"گفتگوی تازه ربات"}})
	if second.Code != 303 {
		t.Fatal(second.Code)
	}
	if got := b.post(second.Header().Get("Location")+"/messages", url.Values{"message": {"چه چیزی ساخته شد؟"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	waitBuilder(t, b, second.Header().Get("Location"), "succeeded")
	last := ""
	for len(requests) > 0 {
		last = <-requests
	}
	if !strings.Contains(last, "سلام تازه") || strings.Contains(last, "حافظه خصوصی کلاس") || strings.Contains(last, "پرسش خصوصی کلاس") {
		t.Fatal("shared Draft or private memory isolation lost")
	}
	detail := b.send("GET", "/bots/1/settings", nil).Body.String()
	if !strings.Contains(detail, "شامل پیام\u200cهای عمومی پیش از ساخت ربات") {
		t.Fatal("deletion confirmation omits general discussion")
	}
	if got := b.post("/bots/1/delete", url.Values{"confirm_delete": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, urlPath := range []string{path, second.Header().Get("Location"), "/bots/1/draft"} {
		if got := b.send("GET", urlPath, nil); got.Code != 404 {
			t.Fatal("Bot deletion retained associated work", urlPath)
		}
	}
	if got := b.send("GET", unrelated, nil); got.Code != 200 {
		t.Fatal("Bot deletion removed unrelated general chat")
	}
}

func TestPikoInitialBuildSupportsApprovedTemplatesWithoutTelegram(t *testing.T) {
	for _, template := range []struct {
		name       string
		definition any
	}{
		{"inquiry", inquiry.Default().Definition()},
		{"registration", registration.Default().Definition()},
		{"booking", booking.Default().Definition()},
	} {
		t.Run(template.name, func(t *testing.T) {
			data, _ := json.Marshal(template.definition)
			var calls atomic.Int64
			_, b := generalBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
				switch calls.Add(1) {
				case 1:
					memoryReply(w, `{"intent":"build"}`)
				case 2:
					builderToolReply(w, "prepare_bot", map[string]string{"name": "ربات فرم", "definition": string(data)})
				default:
					memoryReply(w, "فرم آماده شد")
				}
			})
			path := startPikoChat(t, b)
			if got := b.post(path+"/messages", url.Values{"message": {"ربات فرم بساز"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			waitBuilder(t, b, path, "succeeded")
			if page := b.send("GET", "/bots/1", nil); page.Code != 200 || !strings.Contains(page.Body.String(), "متصل نشده") {
				t.Fatal("build required or fabricated Telegram connection")
			}
			if got := b.post("/bots/1/preview", url.Values{}); got.Code != 303 {
				t.Fatal("Template not previewable")
			}
		})
	}
}

func TestPikoConvertedChatKeepsBotBusyAndRevisionGuards(t *testing.T) {
	staged, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	_, b := generalBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		switch calls.Add(1) {
		case 1:
			memoryReply(w, `{"intent":"build"}`)
		case 2:
			builderToolReply(w, "prepare_bot", map[string]string{"name": "کلاس", "definition": builderFormDraft})
		case 3:
			memoryReply(w, "طرح آماده شد")
		case 4:
			builderToolReply(w, "prepare_draft", map[string]string{"definition": strings.Replace(builderFormDraft, "سلام", "سلام کهنه", 1)})
		default:
			close(staged)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			memoryReply(w, "تغییر کهنه")
		}
	})
	defer close(release)
	path := startPikoChat(t, b)
	b.post(path+"/messages", url.Values{"message": {"ربات بساز"}})
	waitBuilder(t, b, path, "succeeded")
	second := b.post("/bots/1/chats", url.Values{"title": {"دوم"}}).Header().Get("Location")
	if got := b.post(path+"/messages", url.Values{"message": {"پیام تغییر کن"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-staged:
	case <-time.After(3 * time.Second):
		t.Fatal("follow-up not staged")
	}
	if got := b.post(second+"/messages", url.Values{"message": {"همزمان"}}); got.Code != 409 {
		t.Fatal("conversion lost per-Bot active fence", got.Code)
	}
	manual := renderedDraft(t, b.send("GET", "/bots/1/draft", nil).Body.String())
	manual.Set("welcome", "ویرایش دستی تازه")
	if got := b.post("/bots/1/draft", manual); got.Code != 303 {
		t.Fatal("manual editing blocked", got.Code)
	}
	release <- struct{}{}
	page := waitBuilder(t, b, path, "failed")
	if !strings.Contains(page, `data-run-result="conflict"`) || !strings.Contains(b.send("GET", "/bots/1/draft", nil).Body.String(), "ویرایش دستی تازه") {
		t.Fatal("stale converted-chat completion overwrote manual edit")
	}
}

func TestPikoBuildStopCompletionRaceKeepsOnlyCommittedCreation(t *testing.T) {
	for iteration := range 8 {
		t.Run(fmt.Sprint(iteration), func(t *testing.T) {
			staged, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int64
			_, b := generalBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				switch calls.Add(1) {
				case 1:
					memoryReply(w, `{"intent":"build"}`)
				case 2:
					builderToolReply(w, "prepare_bot", map[string]string{"name": "کلاس", "definition": builderFormDraft})
				default:
					close(staged)
					select {
					case <-release:
						memoryReply(w, "طرح آماده شد")
					case <-r.Context().Done():
					}
				}
			})
			path := startPikoChat(t, b)
			b.post(path+"/messages", url.Values{"message": {"ربات کلاس بساز"}})
			select {
			case <-staged:
			case <-time.After(3 * time.Second):
				t.Fatal("not staged")
			}
			if iteration == 1 {
				if got := b.post(path+"/runs/1/stop", url.Values{}); got.Code != 303 {
					t.Fatal(got.Code)
				}
			}
			close(release)
			if iteration == 0 {
				waitBuilder(t, b, path, "succeeded")
			}
			if got := b.post(path+"/runs/1/stop", url.Values{}); got.Code != 303 {
				t.Fatal("Stop lost conversion", got.Code)
			}
			page := b.send("GET", path, nil).Body.String()
			bot := b.send("GET", "/bots/1/draft", nil)
			if strings.Contains(page, `data-run-status="succeeded"`) {
				if bot.Code != 200 || !strings.Contains(page, `data-run-result="created"`) {
					t.Fatal("success missing committed Bot/Draft")
				}
			} else if strings.Contains(page, `data-run-status="stopped"`) {
				if bot.Code != 404 || strings.Contains(page, `data-after-revision=`) || strings.Contains(page, "طرح آماده شد") {
					t.Fatal("Stop retained partial creation")
				}
			} else {
				t.Fatal("race did not save an accurate terminal result")
			}
		})
	}
}
