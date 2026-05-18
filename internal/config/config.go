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

	// --- added in feat/v1.3 ---
	// LiveKitURL       string
	// LiveKitPublicURL string
	// LiveKitAPIKey    string
	// LiveKitSecret    string
}

func Load() (*Config, error) {
	cfg := &Config{
		Port:                env("PORT", "8080"),
		BaseURL:             env("BASE_URL", ""),
		DiscordClientID:     env("DISCORD_CLIENT_ID", ""),
		DiscordClientSecret: env("DISCORD_CLIENT_SECRET", ""),
		DiscordGuildID:      env("DISCORD_GUILD_ID", ""),
		DatabaseURL:         env("DATABASE_URL", ""),
	}

	required := map[string]string{
		"BASE_URL":              cfg.BaseURL,
		"DISCORD_CLIENT_ID":     cfg.DiscordClientID,
		"DISCORD_CLIENT_SECRET": cfg.DiscordClientSecret,
		"DISCORD_GUILD_ID":      cfg.DiscordGuildID,
		"DATABASE_URL":          cfg.DatabaseURL,
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
