package builder_test

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pooya79/Piko/internal/builder"
	"github.com/pooya79/Piko/internal/platform/database"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestFlowTabShowsCommittedSequentialFormAndMessagePaths(t *testing.T) {
	a, b := fixture.DraftFixture(t)
	b.PostDraft(t, "/bots/1/draft", fixture.InquiryDraft())
	fixture.SeedBotChat(t, a.DB, 1, "جریان واقعی")
	page := b.Send("GET", "/bots/1/flow", nil)
	for _, want := range []string{`data-flow-page`, `data-flow-revision="1"`, `data-flow-key="block:d2VsY29tZQ"`, `data-flow-key="question:aW5xdWlyeQ:bmFtZQ"`, `data-flow-key="review:aW5xdWlyeQ"`, `data-flow-key="ack:aW5xdWlyeQ"`, "نام شما چیست؟", "شماره تماس", "درخواست شما دریافت شد"} {
		if page.Code != 200 || !strings.Contains(page.Body.String(), want) {
			t.Fatalf("committed Flow missing %s (status %d)", want, page.Code)
		}
	}

	ack := page.Body.String()[strings.Index(page.Body.String(), `data-flow-detail="ack:aW5xdWlyeQ"`):]
	ack = ack[:strings.Index(ack, "</article>")]
	if !strings.Contains(ack, `data-flow-target="block:d2VsY29tZQ"`) {
		t.Fatalf("restart skipped the actual Welcome Block: %s", ack)
	}
	b.PostDraft(t, "/bots/1/draft", fixture.WelcomeDraft())
	page = b.Send("GET", "/bots/1/flow", nil)
	if !strings.Contains(page.Body.String(), `data-flow-revision="2"`) || !strings.Contains(page.Body.String(), "Call 02112345678") || strings.Contains(page.Body.String(), `data-flow-key="review:aW5xdWlyeQ"`) {
		t.Fatal("Flow did not reconcile from the manually committed Draft")
	}
}

func TestSelectedBlockRequestCarriesAuthorizedCommittedContextAndRejectsStaleSelection(t *testing.T) {
	var calls atomic.Int64
	received := make(chan string, 4)
	a, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		calls.Add(1)
		received <- string(data)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"بررسی شد"},"finish_reason":"stop"}]}`))
	})
	values := url.Values{"message": {"این متن را کوتاه کن"}, "request_key": {"flow-request"}, "selected_block": {"block:d2VsY29tZQ"}, "selected_revision": {"1"}}
	if got := b.Post("/bots/1/chats/1/messages", values); got.Code != 303 {
		t.Fatal(got.Code)
	}
	fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	request := <-received
	for _, want := range []string{`Selected Block context`, `block:d2VsY29tZQ`, `"bot_id":1`, `"revision":1`, `welcome`} {
		// Provider JSON escapes nested text; decode the wire message first.
		var wire struct {
			Messages []struct{ Content json.RawMessage }
		}
		if err := json.Unmarshal([]byte(request), &wire); err != nil {
			t.Fatal(err)
		}
		all := ""
		for _, m := range wire.Messages {
			var text string
			if json.Unmarshal(m.Content, &text) == nil {
				all += text
			}
		}
		if !strings.Contains(all, want) {
			t.Fatalf("provider missing authorized context %s", want)
		}
	}
	b.PostDraft(t, "/bots/1/draft", fixture.WelcomeDraft())
	// A lost successful response can be deduplicated even after the Draft changes.
	if got := b.Post("/bots/1/chats/1/messages", values); got.Code != 303 {
		t.Fatalf("duplicate: %d", got.Code)
	}
	values.Set("request_key", "flow-stale")
	got := b.Post("/bots/1/chats/1/messages", values)
	if got.Code != 409 || !strings.Contains(got.Body.String(), "درخواست ارسال نشد") || !strings.Contains(got.Body.String(), "این متن را کوتاه کن") {
		t.Fatalf("stale context: %d", got.Code)
	}
	values.Set("selected_revision", "2")
	values.Set("selected_block", "question:aW5xdWlyeQ:bmFtZQ")
	if got := b.Post("/bots/1/chats/1/messages", values); got.Code != 409 {
		t.Fatalf("removed target: %d", got.Code)
	}
	values.Set("selected_block", "block:d2VsY29tZQ")
	values.Set("selected_revision", "bad")
	if got := b.Post("/bots/1/chats/1/messages", values); got.Code != 422 {
		t.Fatalf("malformed target: %d", got.Code)
	}
	values.Set("selected_revision", "2")
	values.Set("csrf_token", "wrong")
	if got := fixture.StudioRequest(b, "POST", "/bots/1/chats/1/messages", values); got.Code != 403 {
		t.Fatalf("CSRF: %d", got.Code)
	}
	values.Del("csrf_token")
	other := fixture.NewAccountBrowser(t, a.Handler)
	other.Send("GET", "/register", nil)
	other.Post("/register", fixture.RegisterValues("flow-other@example.test", "Other", "OwnerPassword123"))
	if got := other.Post("/bots/1/chats/1/messages", values); got.Code != 404 {
		t.Fatalf("owner: %d", got.Code)
	}
	if got := fixture.StudioRequest(other, "GET", "/bots/1/chats/1", nil); got.Code != 404 || strings.Contains(got.Body.String(), "data-studio-flow") {
		t.Fatal("Flow leaked owner data")
	}
	if calls.Load() != 1 {
		t.Fatal("rejected selections or duplicate invoked provider")
	}
}

func TestFlowInspectionScopesQuestionIdentityToItsForm(t *testing.T) {
	received := make(chan string, 1)
	_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		received <- string(data)
		fixture.MemoryReply(w, "بررسی شد")
	})
	values := fixture.CombinedDraft()
	values["question_id"][3] = "name" // Both Forms may legitimately ask a Question with this ID.
	if got := b.PostDraft(t, "/bots/1/draft", values); got.Code != 303 {
		t.Fatal(got.Code)
	}
	page := b.Send("GET", "/bots/1/flow", nil).Body.String()
	selected := "question:" + base64.RawURLEncoding.EncodeToString([]byte("registration")) + ":bmFtZQ"
	for _, want := range []string{`data-flow-key="` + selected + `"`, `data-flow-key="question:aW5xdWlyeQ:bmFtZQ"`, "هنر", "علوم", "عدد", "تاریخ شمسی", "رد کردن"} {
		if !strings.Contains(page, want) {
			t.Fatalf("missing real Question context %s", want)
		}
	}
	if got := fixture.StudioRequest(b, "POST", "/bots/1/chats/1/messages", url.Values{"message": {"گزینه\u200cها را تغییر بده"}, "selected_block": {selected}, "selected_revision": {"2"}}); got.Code != 200 {
		t.Fatal(got.Code)
	}
	fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
	var req struct {
		Messages []struct{ Content json.RawMessage }
	}
	if err := json.Unmarshal([]byte(<-received), &req); err != nil {
		t.Fatal(err)
	}
	var system string
	_ = json.Unmarshal(req.Messages[0].Content, &system)
	at := strings.Index(system, "Selected Block context")
	if at < 0 {
		t.Fatal("missing selection")
	}
	for _, want := range []string{`"form_id":"registration"`, `"id":"name"`, `single_choice`, `هنر`} {
		if !strings.Contains(system[at:], want) {
			t.Fatalf("wrong Form selection: %s", want)
		}
	}
}

func TestSelectedBlockRetryRevalidatesAfterRestartAndPreservesHistoricalRuns(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "unchanged", true: "stale"}[changed], func(t *testing.T) {
			started := make(chan struct{})
			received := make(chan string, 1)
			var calls atomic.Int64
			a, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				call := calls.Add(1)
				if call == 1 {
					fixture.MemoryReply(w, "سابقه پیش از ارتقا")
					return
				}
				if call == 2 {
					close(started)
					<-r.Context().Done()
					return
				}
				received <- string(data)
				fixture.MemoryReply(w, "پاسخ بازیابی")
			})
			if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"گفتگوی پیشین"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
			// Model a pre-selection installation without replacing accounts/Bots/Drafts.
			fixture.RollbackToMigration(t, a.DB, "000020_preview_source_revision")
			if err := database.Migrate(t.Context(), a.DB, false); err != nil {
				t.Fatal(err)
			}
			if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"درخواست مشخص"}, "selected_block": {"block:d2VsY29tZQ"}, "selected_revision": {"1"}}); got.Code != 303 {
				t.Fatal(got.Code)
			}
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("provider not started")
			}
			a.StopWork()
			a.Builder.Wait()
			_ = a.DB.Close()
			restarted, err := fixture.New(t.Context(), a.Config)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { restarted.StopWork(); restarted.Builder.Wait(); _ = restarted.DB.Close() })
			b.Router = restarted.Handler
			page := b.Send("GET", "/bots/1/chats/1", nil)
			if page.Code != 200 || (!strings.Contains(page.Body.String(), "درخواست مشخص") || !strings.Contains(page.Body.String(), "سابقه پیش از ارتقا")) || !strings.Contains(page.Body.String(), `data-run-status="interrupted"`) {
				t.Fatal("restart lost saved request")
			}
			if changed {
				b.PostDraft(t, "/bots/1/draft", fixture.WelcomeDraft())
			}
			got := fixture.StudioRequest(b, "POST", "/bots/1/chats/1/runs/2/retry", nil)
			if changed {
				if got.Code != 409 || calls.Load() != 2 || !strings.Contains(got.Body.String(), "درخواست ارسال نشد") {
					t.Fatal("retry silently targeted changed Draft")
				}
				return
			}
			if got.Code != 200 {
				t.Fatal(got.Code)
			}
			fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
			if !strings.Contains(<-received, "Selected Block context") || calls.Load() != 3 {
				t.Fatal("retry lost selected context")
			}
		})
	}
}

func TestSelectedBlockAcceptsMaximumApprovedUnicodeIdentities(t *testing.T) {
	_, b := fixture.BuilderFixture(t, builder.Config{}, func(w http.ResponseWriter, r *http.Request) { fixture.MemoryReply(w, "بررسی شد") })
	var d map[string]any
	if err := json.Unmarshal([]byte(fixture.BuilderFormDraft), &d); err != nil {
		t.Fatal(err)
	}
	formID, questionID := strings.Repeat("🦉", 64), strings.Repeat("🌙", 64)
	form := d["forms"].([]any)[0].(map[string]any)
	form["id"] = formID
	form["questions"].([]any)[0].(map[string]any)["id"] = questionID
	d["menu"].(map[string]any)["choices"].([]any)[0].(map[string]any)["target"] = formID
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.PostDraft(t, "/bots/1/draft", url.Values{"definition": {string(data)}}); got.Code != 303 {
		t.Fatal(got.Code)
	}
	key := "question:" + base64.RawURLEncoding.EncodeToString([]byte(formID)) + ":" + base64.RawURLEncoding.EncodeToString([]byte(questionID))
	if got := b.Post("/bots/1/chats/1/messages", url.Values{"message": {"این پرسش را بررسی کن"}, "selected_block": {key}, "selected_revision": {"2"}}); got.Code != 303 {
		t.Fatalf("approved Unicode identity rejected: %d", got.Code)
	}
	fixture.WaitBuilder(t, b, "/bots/1/chats/1", "succeeded")
}
