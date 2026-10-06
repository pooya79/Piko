package builder_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/bot/templates/booking"
	"github.com/pooya79/Piko/internal/bot/templates/inquiry"
	"github.com/pooya79/Piko/internal/bot/templates/registration"
	"github.com/pooya79/Piko/internal/builder"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestPikoBuildCreatesBotInOriginalChatOnce(t *testing.T) {
	var calls atomic.Int64
	_, b := fixture.GeneralBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			fixture.MemoryReply(w, `{"intent":"build"}`)
		case 2:
			fixture.BuilderToolReply(w, "prepare_bot", map[string]string{"name": "ثبت نام کلاس", "definition": fixture.BuilderFormDraft})
		default:
			fixture.MemoryReply(w, "طرح ثبت نام آماده شد؛ پیش نمایش را امتحان کنید.")
		}
	})
	path := fixture.StartPikoChat(t, b)
	values := url.Values{"message": {"یک ربات ثبت نام کلاس بساز"}, "request_key": {"initial-build-1"}}
	if got := b.Post(path+"/messages", values); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page := fixture.WaitBuilder(t, b, path, "succeeded")
	if !strings.Contains(page, `href="/bots/1/draft"`) || !strings.Contains(page, "ثبت نام کلاس") || !strings.Contains(page, values.Get("message")) {
		t.Fatal("original chat did not become Bot workspace", page)
	}
	if pane := fixture.ChangesPane(t, page); !strings.Contains(pane, `data-change-result="created"`) || !strings.Contains(pane, `data-change-action="added"`) || strings.Contains(pane, "/undo") {
		t.Fatal("initial committed Draft missing truthful Changes or offers Undo without a prior Draft")
	}
	if got := b.Send("GET", "/bots/1/draft", nil); got.Code != 200 || !strings.Contains(got.Body.String(), "ثبت نام") {
		t.Fatal("initial Draft missing")
	}
	for range 2 {
		if got := b.Post(path+"/messages", values); got.Code != 303 {
			t.Fatal("duplicate", got.Code)
		}
		if got := b.Send("GET", path+"/status", nil); got.Code != 200 {
			t.Fatal(got.Code)
		}
	}
	if calls.Load() != 3 || b.Send("GET", "/bots/2", nil).Code != 404 {
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
			a, b := fixture.GeneralBuilderFixture(t, config, "error", func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				switch calls.Add(1) {
				case 1:
					fixture.MemoryReply(w, `{"intent":"build"}`)
				case 2:
					if fault == "missing-stage" {
						fixture.MemoryReply(w, "پاسخ موفق ذخیره نشده")
						return
					}
					definition, name := fixture.BuilderFormDraft, "ربات کلاس"
					if fault == "invalid" {
						definition = `{"version":99}`
					}
					if fault == "name" {
						name = strings.Repeat("س", 81)
					}
					fixture.BuilderToolReply(w, "prepare_bot", map[string]string{"name": name, "definition": definition})
				default:
					switch fault {
					case "provider":
						w.WriteHeader(500)
					case "tool":
						fixture.BuilderToolReply(w, "unsupported_tool", map[string]string{})
					case "truncated":
						_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ناقص"},"finish_reason":"length"}]}`))
					case "timeout":
						<-r.Context().Done()
					default:
						fixture.MemoryReply(w, "پاسخ موفق ذخیره نشده")
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
				if _, err := a.DB.Exec(trigger); err != nil {
					t.Fatal(err)
				}
			}
			path := fixture.StartPikoChat(t, b)
			values := url.Values{"message": {"ربات کلاس بساز"}, "request_key": {"failure-request"}}
			if got := b.Post(path+"/messages", values); got.Code != 303 {
				t.Fatal(got.Code)
			}
			status := "failed"
			if fault == "timeout" {
				status = "timeout"
			}
			page := fixture.WaitBuilder(t, b, path, status)
			if !strings.Contains(page, values.Get("message")) || strings.Contains(page, "پاسخ موفق ذخیره نشده") || strings.Contains(page, "private storage failure") || strings.Contains(page, `data-draft-revision=`) {
				t.Fatal("failure lost conversation or leaked partial success")
			}
			if strings.Contains(page, `data-change-action=`) || strings.Contains(page, "/undo") {
				t.Fatal("failed initial creation displays saved Changes or Undo")
			}
			if got := b.Send("GET", "/bots/1", nil); got.Code != 404 {
				t.Fatal("failure left a Bot")
			}
			before := calls.Load()
			if got := b.Post(path+"/messages", values); got.Code != 303 {
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
			a, b := fixture.GeneralBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				switch calls.Add(1) {
				case 1:
					fixture.MemoryReply(w, `{"intent":"build"}`)
				case 2:
					fixture.BuilderToolReply(w, "prepare_bot", map[string]string{"name": "کلاس", "definition": fixture.BuilderFormDraft})
				default:
					close(staged)
					<-r.Context().Done()
				}
			})
			path := fixture.StartPikoChat(t, b)
			values := url.Values{"message": {"ربات بساز"}, "request_key": {"staged-request"}}
			if got := b.Post(path+"/messages", values); got.Code != 303 {
				t.Fatal(got.Code)
			}
			select {
			case <-staged:
			case <-time.After(3 * time.Second):
				t.Fatal("candidate not staged")
			}
			status := "stopped"
			if ending == "stop" {
				if got := b.Post(path+"/runs/1/stop", url.Values{}); got.Code != 303 {
					t.Fatal(got.Code)
				}
			} else {
				a.StopWork()
				a.Builder.Wait()
				_ = a.DB.Close()
				restarted, err := fixture.New(context.Background(), a.Config)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { restarted.StopWork(); restarted.Builder.Wait(); _ = restarted.DB.Close() })
				b.Router = restarted.Handler
				status = "interrupted"
			}
			page := fixture.WaitBuilder(t, b, path, status)
			if !strings.Contains(page, values.Get("message")) || b.Send("GET", "/bots/1", nil).Code != 404 {
				t.Fatal("cancellation persisted staged Bot or lost history")
			}
			server := httptest.NewServer(b.Router)
			defer server.Close()
			for range 2 {
				req, _ := http.NewRequestWithContext(t.Context(), "GET", server.URL+path+"/stream", nil)
				for _, cookie := range b.Jar.Cookies(b.Base) {
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
			if got := b.Post(path+"/messages", values); got.Code != 303 || calls.Load() != 3 {
				t.Fatal("reconnect or duplicate replayed staged creation")
			}
		})
	}
}

func TestPikoBuildStreamObservesConversionWithoutReadmission(t *testing.T) {
	staged, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	_, b := fixture.GeneralBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		switch calls.Add(1) {
		case 1:
			fixture.MemoryReply(w, `{"intent":"build"}`)
		case 2:
			fixture.BuilderToolReply(w, "prepare_bot", map[string]string{"name": "کلاس", "definition": fixture.BuilderFormDraft})
		default:
			close(staged)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			fixture.MemoryReply(w, "طرح آماده شد")
		}
	})
	defer close(release)
	path := fixture.StartPikoChat(t, b)
	values := url.Values{"message": {"ربات کلاس بساز"}, "request_key": {"stream-build"}}
	if got := b.Post(path+"/messages", values); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-staged:
	case <-time.After(3 * time.Second):
		t.Fatal("not staged")
	}
	if got := b.Post(path+"/messages", values); got.Code != 303 {
		t.Fatal("duplicate active request", got.Code)
	}
	server := httptest.NewServer(b.Router)
	defer server.Close()
	req, _ := http.NewRequestWithContext(t.Context(), "GET", server.URL+path+"/stream", nil)
	for _, cookie := range b.Jar.Cookies(b.Base) {
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
	a, b := fixture.GeneralBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- string(body)
		if strings.Contains(string(body), "Summarize older messages") {
			fixture.MemoryReply(w, "حافظه خصوصی کلاس")
			return
		}
		switch calls.Add(1) {
		case 1, 2, 3, 4, 5, 6, 7:
			fixture.MemoryReply(w, "پاسخ عمومی")
		case 8:
			fixture.MemoryReply(w, `{"intent":"build"}`)
		case 9:
			fixture.BuilderToolReply(w, "prepare_bot", map[string]string{"name": "کلاس زبان", "definition": fixture.BuilderFormDraft})
		case 10:
			fixture.MemoryReply(w, "طرح کلاس آماده شد")
		case 11:
			fixture.BuilderToolReply(w, "read_draft", map[string]string{})
		case 12:
			fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": strings.Replace(fixture.BuilderFormDraft, "سلام", "سلام تازه", 1)})
		default:
			fixture.MemoryReply(w, "پاسخ ادامه کلاس")
		}
	})
	path, unrelated := fixture.StartPikoChat(t, b), fixture.StartPikoChat(t, b)
	for turn := range 7 {
		if got := b.Post(path+"/messages", url.Values{"message": {fixture.MemoryText(fmt.Sprintf("پرسش خصوصی کلاس %d", turn), 13000)}}); got.Code != 303 {
			t.Fatal(got.Code)
		}
		fixture.WaitBuilder(t, b, path, "succeeded")
	}
	for len(requests) > 0 {
		<-requests
	}
	values := url.Values{"message": {"حالا ربات کلاس بساز"}, "request_key": {"memory-build"}}
	if got := b.Post(path+"/messages", values); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page := fixture.WaitBuilder(t, b, path, "succeeded")
	if !strings.Contains(page, "پرسش خصوصی کلاس 0") || !strings.Contains(page, `data-run-result="created"`) {
		t.Fatal("conversion lost earlier conversation")
	}
	for len(requests) > 0 {
		if request := <-requests; !strings.Contains(request, "Summarize older messages") && !strings.Contains(request, "حافظه خصوصی کلاس") {
			t.Fatal("conversion lost private summary")
		}
	}
	other := fixture.NewAccountBrowser(t, b.Router)
	other.Send("GET", "/register", nil)
	if got := other.Post("/register", fixture.RegisterValues("other-build@example.test", "دیگری", "OwnerPassword123")); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, urlPath := range []string{path, path + "/status", "/bots/1/chats/1", "/bots/1/draft"} {
		if got := other.Send("GET", urlPath, nil); got.Code != 404 {
			t.Fatal("cross-owner read", urlPath, got.Code)
		}
	}
	for _, urlPath := range []string{path + "/messages", "/bots/1/name"} {
		if got := other.Post(urlPath, url.Values{"message": {"بساز"}, "request_key": {"memory-build"}, "name": {"نام دیگری"}}); got.Code != 404 {
			t.Fatal("cross-owner transition", got.Code)
		}
	}
	if got := b.Send("GET", "/bots/1/name", nil); got.Code != 405 {
		t.Fatal("rename accepts GET")
	}
	if got := b.Send("POST", "/bots/1/name", url.Values{"name": {"نام تازه"}, "csrf_token": {"wrong"}}); got.Code != 403 {
		t.Fatal("rename lacks CSRF")
	}
	for _, name := range []string{"", strings.Repeat("س", 81), "bad\nname"} {
		if got := b.Post("/bots/1/name", url.Values{"name": {name}}); got.Code != 422 {
			t.Fatal("invalid name accepted", got.Code)
		}
	}
	if got := b.Post("/bots/1/name", url.Values{"name": {"نام تازه"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	if page := b.Send("GET", path, nil); !strings.Contains(page.Body.String(), "نام تازه") {
		t.Fatal("workspace name not editable")
	}
	// Saved creation, deduplication and original links survive a process restart.
	a.StopWork()
	a.Builder.Wait()
	_ = a.DB.Close()
	restarted, err := fixture.New(t.Context(), a.Config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restarted.StopWork(); restarted.Builder.Wait(); _ = restarted.DB.Close() })
	b.Router = restarted.Handler
	if got := b.Post(path+"/messages", values); got.Code != 303 || calls.Load() != 10 {
		t.Fatal("restart replayed committed creation")
	}
	if got := b.Post(path+"/messages", url.Values{"message": {"پیام خوش آمد تازه کن"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page = fixture.WaitBuilder(t, b, path, "succeeded")
	if !strings.Contains(page, `data-run-result="saved"`) || !strings.Contains(page, `data-draft-revision="2"`) {
		t.Fatal("converted chat cannot edit shared Draft")
	}
	if got := b.Post("/bots/1/preview", url.Values{}); got.Code != 303 || b.Send("GET", got.Header().Get("Location"), nil).Code != 200 {
		t.Fatal("converted Draft cannot Preview")
	}
	second := fixture.SeedBotChat(t, restarted.DB, 1, "گفتگوی تازه ربات")
	if got := b.Post(second+"/messages", url.Values{"message": {"چه چیزی ساخته شد؟"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	fixture.WaitBuilder(t, b, second, "succeeded")
	last := ""
	for len(requests) > 0 {
		last = <-requests
	}
	if !strings.Contains(last, "سلام تازه") || strings.Contains(last, "حافظه خصوصی کلاس") || strings.Contains(last, "پرسش خصوصی کلاس") {
		t.Fatal("shared Draft or private memory isolation lost")
	}
	detail := b.Send("GET", "/bots/1/settings/delete", nil).Body.String()
	if !strings.Contains(detail, "شامل پیام\u200cهای عمومی پیش از ساخت ربات") {
		t.Fatal("deletion confirmation omits general discussion")
	}
	if got := b.Post("/bots/1/delete", url.Values{"confirm_delete": {"yes"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	for _, urlPath := range []string{path, second, "/bots/1/draft"} {
		if got := b.Send("GET", urlPath, nil); got.Code != 404 {
			t.Fatal("Bot deletion retained associated work", urlPath)
		}
	}
	if got := b.Send("GET", unrelated, nil); got.Code != 200 {
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
			_, b := fixture.GeneralBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
				switch calls.Add(1) {
				case 1:
					fixture.MemoryReply(w, `{"intent":"build"}`)
				case 2:
					fixture.BuilderToolReply(w, "prepare_bot", map[string]string{"name": "ربات فرم", "definition": string(data)})
				default:
					fixture.MemoryReply(w, "فرم آماده شد")
				}
			})
			path := fixture.StartPikoChat(t, b)
			if got := b.Post(path+"/messages", url.Values{"message": {"ربات فرم بساز"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			fixture.WaitBuilder(t, b, path, "succeeded")
			if page := b.Send("GET", "/bots/1", nil); page.Code != 200 || !strings.Contains(page.Body.String(), "متصل نشده") {
				t.Fatal("build required or fabricated Telegram connection")
			}
			if got := b.Post("/bots/1/preview", url.Values{}); got.Code != 303 {
				t.Fatal("Template not previewable")
			}
		})
	}
}

func TestPikoConvertedChatKeepsBotBusyAndRevisionGuards(t *testing.T) {
	staged, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	a, b := fixture.GeneralBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		switch calls.Add(1) {
		case 1:
			fixture.MemoryReply(w, `{"intent":"build"}`)
		case 2:
			fixture.BuilderToolReply(w, "prepare_bot", map[string]string{"name": "کلاس", "definition": fixture.BuilderFormDraft})
		case 3:
			fixture.MemoryReply(w, "طرح آماده شد")
		case 4:
			fixture.BuilderToolReply(w, "prepare_draft", map[string]string{"definition": strings.Replace(fixture.BuilderFormDraft, "سلام", "سلام کهنه", 1)})
		default:
			close(staged)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			fixture.MemoryReply(w, "تغییر کهنه")
		}
	})
	defer close(release)
	path := fixture.StartPikoChat(t, b)
	b.Post(path+"/messages", url.Values{"message": {"ربات بساز"}})
	fixture.WaitBuilder(t, b, path, "succeeded")
	second := fixture.SeedBotChat(t, a.DB, 1, "دوم")
	if got := b.Post(path+"/messages", url.Values{"message": {"پیام تغییر کن"}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	select {
	case <-staged:
	case <-time.After(3 * time.Second):
		t.Fatal("follow-up not staged")
	}
	if got := b.Post(second+"/messages", url.Values{"message": {"همزمان"}}); got.Code != 409 {
		t.Fatal("conversion lost per-Bot active fence", got.Code)
	}
	manual := fixture.RenderedDraft(t, b.Send("GET", "/bots/1/draft", nil).Body.String())
	manual.Set("welcome", "ویرایش دستی تازه")
	if got := b.Post("/bots/1/draft", manual); got.Code != 303 {
		t.Fatal("manual editing blocked", got.Code)
	}
	release <- struct{}{}
	page := fixture.WaitBuilder(t, b, path, "failed")
	if !strings.Contains(page, `data-run-result="conflict"`) || !strings.Contains(b.Send("GET", "/bots/1/draft", nil).Body.String(), "ویرایش دستی تازه") {
		t.Fatal("stale converted-chat completion overwrote manual edit")
	}
}

func TestPikoBuildStopCompletionRaceKeepsOnlyCommittedCreation(t *testing.T) {
	for iteration := range 8 {
		t.Run(fmt.Sprint(iteration), func(t *testing.T) {
			staged, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int64
			_, b := fixture.GeneralBuilderFixture(t, builder.Config{}, "error", func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				switch calls.Add(1) {
				case 1:
					fixture.MemoryReply(w, `{"intent":"build"}`)
				case 2:
					fixture.BuilderToolReply(w, "prepare_bot", map[string]string{"name": "کلاس", "definition": fixture.BuilderFormDraft})
				default:
					close(staged)
					select {
					case <-release:
						fixture.MemoryReply(w, "طرح آماده شد")
					case <-r.Context().Done():
					}
				}
			})
			path := fixture.StartPikoChat(t, b)
			b.Post(path+"/messages", url.Values{"message": {"ربات کلاس بساز"}})
			select {
			case <-staged:
			case <-time.After(3 * time.Second):
				t.Fatal("not staged")
			}
			if iteration == 1 {
				if got := b.Post(path+"/runs/1/stop", url.Values{}); got.Code != 303 {
					t.Fatal(got.Code)
				}
			}
			close(release)
			if iteration == 0 {
				fixture.WaitBuilder(t, b, path, "succeeded")
			}
			if got := b.Post(path+"/runs/1/stop", url.Values{}); got.Code != 303 {
				t.Fatal("Stop lost conversion", got.Code)
			}
			page := b.Send("GET", path, nil).Body.String()
			bot := b.Send("GET", "/bots/1/draft", nil)
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
