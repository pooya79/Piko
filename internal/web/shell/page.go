package shell

import (
	"context"

	"github.com/pooya79/Piko/internal/locale"
)

// Page supplies feature-owned identity to the shared workspace. Its zero value
// preserves the Dashboard identity and disabled Bot navigation.
type Page struct {
	Title       string
	Breadcrumbs []Breadcrumb
	ActiveNav   NavSection
	// Set BotsURL only when the Bot list route is implemented. RecentBots must
	// come from owner-authorized feature data; the shell supplies no demo data.
	BotsURL      string
	CreateBotURL string
	BuilderURL   string
	RecentBots   []BotLink
	// BotURL is supplied only after the feature authorizes the requested Bot.
	BotURL     string
	BotSection BotNavSection
}

type navigationKey struct{}

// WithNavigation carries owner-authorized links to workspace renderers.
func WithNavigation(ctx context.Context, page Page) context.Context {
	return context.WithValue(ctx, navigationKey{}, page)
}

type NavSection string

const (
	DashboardNav NavSection = ""
	BotsNav      NavSection = "bots"
	BuilderNav   NavSection = "builder"
)

type BotNavSection string

const (
	BotOverview    BotNavSection = "overview"
	BotStudio      BotNavSection = "studio"
	BotFlow        BotNavSection = "flow"
	BotSubmissions BotNavSection = "submissions"
	BotConnection  BotNavSection = "connection"
	BotSettings    BotNavSection = "settings"
	BotDraft       BotNavSection = "draft"
)

// BotLink contains only navigation identity, never inferred status or metrics.
type BotLink struct {
	Name string
	URL  string
}

// Breadcrumb labels are plain text. An empty URL renders a non-navigable label;
// callers supply URLs only for implemented, authorized feature destinations.
// The final breadcrumb always identifies the current page without a link.
type Breadcrumb struct {
	Label string
	URL   string
}

func (p Page) withDefaults(ctx context.Context) Page {
	if nav, ok := ctx.Value(navigationKey{}).(Page); ok && p.BotsURL == "" {
		p.BotsURL, p.CreateBotURL, p.RecentBots = nav.BotsURL, nav.CreateBotURL, nav.RecentBots
		p.BuilderURL = nav.BuilderURL
	}
	if p.Title == "" {
		p.Title = locale.T(ctx, "dashboard.title")
	}
	if len(p.Breadcrumbs) == 0 {
		p.Breadcrumbs = []Breadcrumb{{Label: locale.T(ctx, "workspace.title")}, {Label: p.Title}}
	}
	return p
}
