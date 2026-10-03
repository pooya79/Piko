package locale

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

const CookieName = "piko_language"

//go:embed en.json fa.json
var catalogs embed.FS

var semanticKey = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z][a-z0-9]*)+$`)

type Catalog struct {
	texts  map[string]map[string]string
	keys   map[string]struct{}
	source map[string]string
}

type requestLocale struct {
	catalog  *Catalog
	language string
	returnTo string
}

type contextKey struct{}

// NewCatalog validates both embedded catalogs before any request can render.
func NewCatalog() (*Catalog, error) {
	english, err := catalogs.ReadFile("en.json")
	if err != nil {
		return nil, err
	}
	persian, err := catalogs.ReadFile("fa.json")
	if err != nil {
		return nil, err
	}
	return newCatalog(english, persian)
}

func newCatalog(english, persian []byte) (*Catalog, error) {
	bundle := i18n.NewBundle(language.English)
	loaded := make(map[string]map[string]*i18n.Message, 2)
	for _, entry := range []struct {
		lang string
		data []byte
	}{{"en", english}, {"fa", persian}} {
		lang, data := entry.lang, entry.data
		messages := make(map[string]*i18n.Message)
		if err := json.Unmarshal(data, &messages); err != nil {
			return nil, fmt.Errorf("parse %s catalog: %w", lang, err)
		}
		if len(messages) == 0 {
			return nil, fmt.Errorf("empty %s catalog", lang)
		}
		loaded[lang] = messages
	}
	keys := make(map[string]struct{}, len(loaded["en"]))
	source := make(map[string]string, len(loaded["en"]))
	for key, english := range loaded["en"] {
		if !semanticKey.MatchString(key) {
			return nil, fmt.Errorf("non-semantic catalog key %q", key)
		}
		persian, ok := loaded["fa"][key]
		if !ok {
			return nil, fmt.Errorf("missing Persian catalog key %q", key)
		}
		if english == nil || persian == nil || strings.TrimSpace(english.Other) == "" || strings.TrimSpace(persian.Other) == "" {
			return nil, fmt.Errorf("empty catalog translation %q", key)
		}
		keys[key] = struct{}{}
		if previous, exists := source[english.Other]; exists && loaded["fa"][previous].Other != persian.Other {
			return nil, fmt.Errorf("conflicting Persian translations for %q", english.Other)
		}
		source[english.Other] = key
	}
	for key := range loaded["fa"] {
		if _, ok := keys[key]; !ok {
			return nil, fmt.Errorf("extra Persian catalog key %q", key)
		}
	}
	for lang, messages := range loaded {
		tag := language.English
		if lang == "fa" {
			tag = language.Persian
		}
		ordered := make([]string, 0, len(messages))
		for key := range messages {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		for _, key := range ordered {
			message := messages[key]
			message.ID = key
			if err := bundle.AddMessages(tag, message); err != nil {
				return nil, fmt.Errorf("add %s translation %q: %w", lang, key, err)
			}
		}
	}
	texts := make(map[string]map[string]string, 2)
	for _, lang := range []string{"en", "fa"} {
		localizer := i18n.NewLocalizer(bundle, lang)
		texts[lang] = make(map[string]string, len(keys))
		for key := range keys {
			translated, err := localizer.Localize(&i18n.LocalizeConfig{MessageID: key})
			if err != nil {
				return nil, fmt.Errorf("translate %s key %q: %w", lang, key, err)
			}
			texts[lang][key] = translated
		}
	}
	return &Catalog{texts: texts, keys: keys, source: source}, nil
}

func Supported(language string) bool { return language == "fa" || language == "en" }

// Middleware selects only an explicit supported cookie; first visits use Persian.
func (c *Catalog) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang := "fa"
		if cookie, err := r.Cookie(CookieName); err == nil && Supported(cookie.Value) {
			lang = cookie.Value
		}
		location := r.URL.RequestURI()
		if location == "" {
			location = "/"
		}
		next.ServeHTTP(w, r.WithContext(c.With(r.Context(), lang, location)))
	})
}

// With attaches a validated locale to a request context. The composition root owns the catalog.
func (c *Catalog) With(ctx context.Context, lang, returnTo string) context.Context {
	if c == nil || !Supported(lang) {
		panic("invalid request locale")
	}
	return context.WithValue(ctx, contextKey{}, requestLocale{catalog: c, language: lang, returnTo: returnTo})
}

func current(ctx context.Context) requestLocale {
	value, ok := ctx.Value(contextKey{}).(requestLocale)
	if !ok || value.catalog == nil {
		panic("request locale was not injected")
	}
	return value
}

func Language(ctx context.Context) string { return current(ctx).language }

func Direction(ctx context.Context) string {
	if Language(ctx) == "fa" {
		return "rtl"
	}
	return "ltr"
}

func ReturnTo(ctx context.Context) string { return current(ctx).returnTo }

// T reads copy prepared at startup and panics on a missing key.
func T(ctx context.Context, key string) string {
	value := current(ctx)
	if _, ok := value.catalog.keys[key]; !ok {
		panic(fmt.Sprintf("missing translation key %q", key))
	}
	return value.catalog.texts[value.language][key]
}

// Text resolves a message for a stored account language outside an HTTP request.
func (c *Catalog) Text(language, key string) string {
	if c == nil || !Supported(language) {
		panic("invalid account language")
	}
	if _, ok := c.keys[key]; !ok {
		panic(fmt.Sprintf("missing translation key %q", key))
	}
	return c.texts[language][key]
}

// TranslateSource keeps existing shared-error callers stable while requiring catalog coverage.
func TranslateSource(ctx context.Context, english string) string {
	key, ok := current(ctx).catalog.source[english]
	if !ok {
		panic(fmt.Sprintf("untranslated shared error %q", english))
	}
	return T(ctx, key)
}
