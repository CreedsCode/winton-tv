package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/CreedsCode/winton-tv/internal/auth"
	"github.com/CreedsCode/winton-tv/internal/chat"
	"github.com/CreedsCode/winton-tv/internal/config"
	discordbot "github.com/CreedsCode/winton-tv/internal/discord"
	"github.com/CreedsCode/winton-tv/internal/handlers"
	"github.com/CreedsCode/winton-tv/internal/livekit"
	"github.com/CreedsCode/winton-tv/internal/session"
	"github.com/CreedsCode/winton-tv/internal/store"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
)

func main() {
	// .env is optional — env vars from the OS/compose take precedence.
	_ = godotenv.Load()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("config load failed", "err", err)
		os.Exit(1)
	}

	startCtx, startCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer startCancel()

	st, err := store.New(startCtx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("store init failed", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	sqlDB := stdlib.OpenDBFromPool(st.Pool())
	defer sqlDB.Close()

	sessMgr := session.New(sqlDB, cfg.BaseURL)
	authH := auth.New(cfg, st, sessMgr, logger)

	lk := livekit.New(livekit.Config{
		URL:         cfg.LiveKitURL,
		PublicURL:   cfg.LiveKitPublicURL,
		WhipBaseURL: cfg.WhipBaseURL,
		APIKey:      cfg.LiveKitAPIKey,
		APISecret:   cfg.LiveKitAPISecret,
		Logger:      logger,
	})

	// Discord bot is optional. Without DISCORD_BOT_TOKEN the /c routes
	// 501 but everything else works.
	var bot *discordbot.Bot
	if cfg.DiscordBotToken != "" {
		bot, err = discordbot.New(cfg.DiscordBotToken, cfg.DiscordGuildID, logger)
		if err != nil {
			logger.Error("discord bot init failed (continuing without)", "err", err)
			bot = nil
		} else if err := bot.Start(startCtx); err != nil {
			logger.Error("discord bot start failed (continuing without)", "err", err)
			bot = nil
		} else {
			defer bot.Close()
		}
	} else {
		logger.Info("DISCORD_BOT_TOKEN unset — /c routes disabled")
	}

	chatHub := chat.NewHub()
	hs, err := handlers.New(cfg, st, lk, bot, chatHub, logger)
	if err != nil {
		logger.Error("handlers init failed", "err", err)
		os.Exit(1)
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Logger)
	r.Use(middleware.Timeout(30 * time.Second))
	r.Use(sessMgr.LoadAndSave)
	r.Use(authH.LoadUser)

	// public
	r.Get("/", hs.Index)
	r.Get("/login", hs.Login)
	r.Get("/healthz", hs.Healthz)
	r.Get("/multi", hs.Multi)
	r.Get("/c", hs.CIndex)
	r.Get("/c/{channelID}", hs.CView)
	r.Get("/u/{slug}", hs.Profile)
	r.Get("/api/live-streams", hs.APILiveStreams)
	r.Get("/api/chat/{slug}/history", hs.ChatHistory)
	r.Get("/api/chat/{slug}/stream", hs.ChatStream)
	r.Get("/partials/not-in-guild", hs.PartialNotInGuild)
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))

	// auth
	r.Get("/auth/discord", authH.Start)
	r.Get("/auth/discord/callback", authH.Callback)
	r.Post("/logout", authH.Logout)

	// authed, no slug required
	r.Group(func(r chi.Router) {
		r.Use(authH.RequireSession)
		r.Get("/onboarding", hs.OnboardingGet)
		r.Post("/onboarding", hs.OnboardingPost)
		r.Post("/api/chat/{slug}", hs.ChatSend)
	})

	// authed + slug claimed
	r.Group(func(r chi.Router) {
		r.Use(authH.RequireSlug)
		r.Get("/dashboard", hs.Dashboard)
		r.Post("/dashboard/setup-stream", hs.DashboardSetupStream)
		r.Post("/dashboard/rotate-stream", hs.DashboardRotateStream)
		r.Post("/dashboard/discovery", hs.DashboardSetDiscovery)
		r.Post("/dashboard/metadata", hs.DashboardSetMetadata)
		r.Post("/dashboard/profile", hs.DashboardSetProfile)
		r.Get("/dashboard/live", hs.DashboardLive)
	})

	// admin (requires is_admin)
	r.Group(func(r chi.Router) {
		r.Use(authH.RequireAdmin)
		r.Get("/admin", hs.AdminIndex)
		r.Get("/admin/users", hs.AdminUsers)
		r.Post("/admin/users/{id}/ban", hs.AdminUserBan)
		r.Post("/admin/users/{id}/unban", hs.AdminUserUnban)
		r.Post("/admin/users/{id}/promote", hs.AdminUserPromote)
		r.Post("/admin/users/{id}/demote", hs.AdminUserDemote)
		r.Get("/admin/streams", hs.AdminStreams)
		r.Post("/admin/streams/{slug}/kick", hs.AdminStreamKick)
		r.Get("/admin/audit", hs.AdminAudit)
	})

	// Channel pages — public, no auth. Must be registered LAST so the
	// catch-all {slug} pattern doesn't shadow more specific routes.
	// Handler also guards against reserved slugs as a belt-and-suspenders.
	r.Get("/{slug}", hs.Channel)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("server listening", "addr", srv.Addr, "base_url", cfg.BaseURL)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		logger.Error("server crashed", "err", err)
		os.Exit(1)
	case sig := <-stop:
		logger.Info("shutdown signal received", "signal", sig.String())
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
	logger.Info("bye")
}
