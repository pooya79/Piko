package httpfixture

import (
	"net/url"
	"strconv"
	"strings"
	"testing"
)

const BuilderFormDraft = `{"version":2,"welcome":{"id":"welcome","type":"message","text":"سلام"},"menu":{"id":"menu","type":"menu","text":"انتخاب کنید","choices":[{"id":"collect","label":"فرم دلخواه","target":"custom"}]},"messages":[],"forms":[{"id":"custom","review":"پاسخ\u200cها را بررسی کنید","acknowledgement":"درخواست دریافت شد","questions":[{"id":"name","label":"نام","prompt":"نام شما؟","type":"short_text","required":true,"max_length":5},{"id":"details","label":"توضیح","prompt":"توضیح شما؟","type":"long_text","required":false,"max_length":10},{"id":"phone","label":"تماس","prompt":"شماره تماس؟","type":"phone","required":true},{"id":"quantity","label":"مقدار","prompt":"مقدار درخواستی؟","type":"number","required":true,"number":{"min":"1.5","max":"3"}},{"id":"service","label":"خدمت","prompt":"خدمت دلخواه؟","type":"single_choice","required":true,"options":["اول","دوم"]},{"id":"date","label":"روز","prompt":"روز ترجیحی؟","type":"date","required":true,"date":{"min":"1405/07/01","max":"1405/07/30"}}]}]}`

// All steps enter the same HTTP HTTP boundary as an owner; Preview revision
// checks remain active even when answer validation returns recoverable feedback.
func BuilderPreviewStep(t *testing.T, b *Browser, path string, revision *int, values url.Values, want string) {
	t.Helper()
	values.Set("revision", strconv.Itoa(*revision))
	if got := b.Post(path+"/choose", values); got.Code != 303 {
		t.Fatalf("Preview step %d: %d", *revision, got.Code)
	}
	*revision++
	if page := b.Send("GET", path, nil); page.Code != 200 || !strings.Contains(page.Body.String(), want) {
		t.Fatalf("Preview step %d missing %q", *revision, want)
	}
}
