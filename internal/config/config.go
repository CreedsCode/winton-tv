// Package config loads runtime configuration from environment variables.
//
// Production-first: every required var must be set or Load returns an error.
// No dev fallbacks for credentials/URLs — supply a .env locally if needed.
package config

import (
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
)

type Config struct {
	Port    string
	BaseURL string // full origin, e.g. https://winton.derc.io

	DiscordClientID     string
	DiscordClientSecret string
	DiscordGuildID      string
	// DiscordBotToken is optional. If set, the app connects a Gateway
	// session to track voice channel membership for /c/<channel> features.
	// Without it, those routes 404 but everything else works.
	DiscordBotToken string
	// DiscordInviteURL is the public Discord invite link rendered on the
	// landing page (footer + the "not in guild" modal CTA). Optional —
	// empty value collapses the link to "#" in the template.
	DiscordInviteURL string

	// AdminDiscordIDs is a set of Discord user snowflakes who are auto-
	// promoted to admin on every login. Parsed from ADMIN_DISCORD_IDS
	// env var (comma-separated). Empty set = no auto-promotion (admins
	// managed via /admin UI by an existing admin).
	AdminDiscordIDs map[string]bool

	DatabaseURL string

	// --- LiveKit ---
	// LiveKitURL is the internal SFU URL the *backend* uses for the SDK
	// (RoomService, signing) — typically the docker-network URL like
	// ws://livekit:7880.
	LiveKitURL string
	// LiveKitPublicURL is what the *browser* (viewer JS SDK) connects to.
	// Public, TLS, e.g. wss://livekit.winton.derc.io.
	LiveKitPublicURL string
	LiveKitAPIKey    string
	LiveKitAPISecret string

	// WhipBaseURL is the *public* origin OBS posts WHIP requests to.
	// We construct the full per-user URL as <WhipBaseURL>/<stream_key>
	// instead of trusting LiveKit Ingress's resp.Url field — Ingress
	// only fills that in when its own yaml has whip_base_url set, which
	// doesn't reliably env-interpolate in all deploy setups.
	WhipBaseURL string
}

func Load() (*Config, error) {
	cfg := &Config{
		Port:                env("PORT", "8080"),
		BaseURL:             env("BASE_URL", ""),
		DiscordClientID:     env("DISCORD_CLIENT_ID", ""),
		DiscordClientSecret: env("DISCORD_CLIENT_SECRET", ""),
		DiscordGuildID:      env("DISCORD_GUILD_ID", ""),
		DiscordBotToken:     env("DISCORD_BOT_TOKEN", ""),  // optional
		DiscordInviteURL:    env("DISCORD_INVITE_URL", ""), // optional
		AdminDiscordIDs:     parseIDSet(env("ADMIN_DISCORD_IDS", "")),
		DatabaseURL:         env("DATABASE_URL", ""),
		LiveKitURL:          env("LIVEKIT_URL", ""),
		LiveKitPublicURL:    env("LIVEKIT_PUBLIC_URL", ""),
		LiveKitAPIKey:       env("LIVEKIT_API_KEY", ""),
		LiveKitAPISecret:    env("LIVEKIT_API_SECRET", ""),
		WhipBaseURL:         env("WHIP_BASE_URL", ""),
	}

	required := map[string]string{
		"BASE_URL":              cfg.BaseURL,
		"DISCORD_CLIENT_ID":     cfg.DiscordClientID,
		"DISCORD_CLIENT_SECRET": cfg.DiscordClientSecret,
		"DISCORD_GUILD_ID":      cfg.DiscordGuildID,
		"DATABASE_URL":          cfg.DatabaseURL,
		"LIVEKIT_URL":           cfg.LiveKitURL,
		"LIVEKIT_PUBLIC_URL":    cfg.LiveKitPublicURL,
		"LIVEKIT_API_KEY":       cfg.LiveKitAPIKey,
		"LIVEKIT_API_SECRET":    cfg.LiveKitAPISecret,
		"WHIP_BASE_URL":         cfg.WhipBaseURL,
	}
	var missing []string
	for k, v := range required {
		if v == "" {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("required env vars missing: %s", strings.Join(missing, ", "))
	}

	u, err := url.Parse(cfg.BaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("BASE_URL invalid: %q", cfg.BaseURL)
	}
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")

	return cfg, nil
}

// DiscordCallbackURL is the full redirect URL Discord posts back to.
// Must match a redirect URI registered in the Discord application.
func (c *Config) DiscordCallbackURL() string {
	return c.BaseURL + "/auth/discord/callback"
}

func env(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

// parseIDSet parses a comma-separated list of Discord IDs into a lookup
// set. Empty/whitespace IDs are dropped.
func parseIDSet(s string) map[string]bool {
	out := make(map[string]bool)
	for _, raw := range strings.Split(s, ",") {
		id := strings.TrimSpace(raw)
		if id != "" {
			out[id] = true
		}
	}
	return out
}
