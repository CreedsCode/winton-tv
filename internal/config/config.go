// Package config loads runtime configuration from environment variables.
//
// New fields should default to safe development values so `go run` works
// without any .env file, and should return errors only for values that
// would silently misbehave if missing in production.
package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Port string
	Env  string // "development" | "production"

	// --- added in feat/discord-oauth ---
	// DiscordClientID     string
	// DiscordClientSecret string
	// DiscordGuildID      string
	// DiscordRedirectURL  string

	// --- added in feat/livekit-tokens ---
	// LiveKitURL    string
	// LiveKitAPIKey string
	// LiveKitSecret string

	// --- added in feat/postgres-store ---
	// DatabaseURL string

	// --- added in feat/discord-oauth (sessions) ---
	// SessionSecret string
}

func Load() (*Config, error) {
	cfg := &Config{
		Port: getEnv("PORT", "8080"),
		Env:  strings.ToLower(getEnv("ENV", "development")),
	}

	if cfg.Env != "development" && cfg.Env != "production" {
		return nil, fmt.Errorf("ENV must be 'development' or 'production', got %q", cfg.Env)
	}
	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func (c *Config) IsProd() bool { return c.Env == "production" }
