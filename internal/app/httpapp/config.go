package httpapp

import (
	"time"

	"github.com/pooya79/Piko/internal/builder"
)

type Config struct {
	Environment      string
	HTTPAddr         string
	DatabasePath     string
	SessionSecret    string
	BotEncryptionKey string
	BotPublicURL     string
	LogLevel         string
	TrustedProxy     bool
	CookieSecure     bool
	ShutdownPeriod   time.Duration
	Builder          builder.Config
}
