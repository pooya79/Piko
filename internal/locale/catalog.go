package locale

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

//go:embed fa.json
var catalogs embed.FS

var semanticKey = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z][a-z0-9]*)+$`)

type Catalog struct{ texts map[string]string }
type contextKey struct{}

// NewCatalog validates the Persian copy before any request can render.
func NewCatalog() (*Catalog, error) {
	data, err := catalogs.ReadFile("fa.json")
	if err != nil {
		return nil, err
	}
	return newCatalog(data)
}

func newCatalog(data []byte) (*Catalog, error) {
	var messages map[string]struct {
		Other string `json:"other"`
	}
	if err := json.Unmarshal(data, &messages); err != nil {
		return nil, fmt.Errorf("parse Persian catalog: %w", err)
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("empty Persian catalog")
	}
	texts := make(map[string]string, len(messages))
	for key, message := range messages {
		if !semanticKey.MatchString(key) {
			return nil, fmt.Errorf("non-semantic catalog key %q", key)
		}
		if strings.TrimSpace(message.Other) == "" {
			return nil, fmt.Errorf("empty Persian copy %q", key)
		}
		texts[key] = message.Other
	}
	return &Catalog{texts: texts}, nil
}

// Middleware injects Persian copy regardless of legacy cookies or account preferences.
func (c *Catalog) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(c.With(r.Context())))
	})
}

func (c *Catalog) With(ctx context.Context) context.Context {
	if c == nil {
		panic("request catalog is nil")
	}
	return context.WithValue(ctx, contextKey{}, c)
}

// T reads validated copy and fails on an unknown key.
func T(ctx context.Context, key string) string {
	catalog, ok := ctx.Value(contextKey{}).(*Catalog)
	if !ok || catalog == nil {
		panic("request catalog was not injected")
	}
	text, ok := catalog.texts[key]
	if !ok {
		panic(fmt.Sprintf("missing Persian copy key %q", key))
	}
	return text
}
