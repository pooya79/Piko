package auth_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/pooya79/Piko/internal/locale"
	fixture "github.com/pooya79/Piko/internal/testsupport/httpfixture"
)

func TestRegistrationFeedbackRetainsInputAndIdentifiesField(t *testing.T) {
	router := fixture.AccountFeedbackRouter(t)
	for _, language := range []string{"en", "fa"} {
		t.Run(language, func(t *testing.T) {
			form := url.Values{"email": {"mina@example.test"}, "display_name": {"Mina مینا"}, "password": {"short"}}
			w := fixture.FeedbackSubmission(t, router, language, "/register", form)
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status=%d", w.Code)
			}
			nodes := fixture.FeedbackElements(t, w.Body.String())
			if fixture.FeedbackAttr(nodes["email"], "value") != "mina@example.test" || fixture.FeedbackAttr(nodes["display-name"], "value") != "Mina مینا" {
				t.Error("non-sensitive input was lost")
			}
			if fixture.FeedbackAttr(nodes["password"], "value") != "" {
				t.Error("password was repopulated")
			}
			if fixture.FeedbackAttr(nodes["password"], "aria-invalid") != "true" || fixture.FeedbackAttr(nodes["password"], "aria-describedby") != "password-hint password-error" || nodes["password-error"] == nil {
				t.Error("password error does not describe the invalid field alongside its hint")
			}
			ctx := fixture.TestLocaleCatalog(t).With(t.Context())
			if !strings.Contains(w.Body.String(), locale.T(ctx, "auth.register.error.password")) {
				t.Error("localized error missing")
			}
		})
	}
}

func TestAccountFeedbackKeepsSensitiveFailuresGeneric(t *testing.T) {
	router := fixture.AccountFeedbackRouter(t)
	for _, language := range []string{"en", "fa"} {
		t.Run(language, func(t *testing.T) {
			w := fixture.FeedbackSubmission(t, router, language, "/login", url.Values{"email": {"invalid-address"}, "password": {"private-password"}})
			nodes := fixture.FeedbackElements(t, w.Body.String())
			if w.Code != 422 || fixture.FeedbackAttr(nodes["email"], "value") != "invalid-address" {
				t.Error("login lost email or validation status")
			}
			if strings.Contains(w.Body.String(), "private-password") || strings.Contains(w.Body.String(), `aria-invalid="true"`) || !strings.Contains(w.Body.String(), `role="alert"`) {
				t.Error("credentials must stay secret with generic form feedback")
			}
		})
	}
}

func TestRegistrationDetailsIdentifyOnlyKnownInvalidField(t *testing.T) {
	router := fixture.AccountFeedbackRouter(t)
	for _, language := range []string{"en", "fa"} {
		for _, tc := range []struct{ field, id, value, key string }{
			{"email", "email", "invalid-address", "auth.register.error.email"},
			{"display_name", "display-name", "   ", "auth.register.error.name"},
			{"display_name", "display-name", strings.Repeat("م", 81), "auth.register.error.name"},
		} {
			t.Run(language+tc.field, func(t *testing.T) {
				form := url.Values{"email": {"mina@example.test"}, "display_name": {"Mina مینا"}, "password": {"private-password-123"}}
				form.Set(tc.field, tc.value)
				w := fixture.FeedbackSubmission(t, router, language, "/register", form)
				nodes := fixture.FeedbackElements(t, w.Body.String())
				if w.Code != 422 || fixture.FeedbackAttr(nodes[tc.id], "aria-invalid") != "true" || fixture.FeedbackAttr(nodes[tc.id], "aria-describedby") != tc.id+"-error" {
					t.Error("known validation did not identify its field")
				}
				ctx := fixture.TestLocaleCatalog(t).With(t.Context())
				if !strings.Contains(w.Body.String(), locale.T(ctx, tc.key)) || strings.Contains(w.Body.String(), "private-password-123") {
					t.Error("localized feedback missing or password echoed")
				}
				for _, id := range []string{"display-name", "email", "password"} {
					if id != tc.id && fixture.FeedbackAttr(nodes[id], "aria-invalid") == "true" {
						t.Errorf("unrelated field %s marked invalid", id)
					}
				}
			})
		}
	}
}
