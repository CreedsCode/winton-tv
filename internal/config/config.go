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
