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
	"github.com/CreedsCode/winton-tv/internal/config"
	"github.com/CreedsCode/winton-tv/internal/handlers"
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

	// scs needs a *sql.DB — wrap the pgxpool with the stdlib adapter.
	sqlDB := stdlib.OpenDBFromPool(st.Pool())
	defer sqlDB.Close()

	sessMgr := session.New(sqlDB, cfg.BaseURL)
	authH := auth.New(cfg, st, sessMgr, logger)

	hs, err := handlers.New(cfg, st, logger)
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
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.Dir("web/static"))))

	// auth
	r.Get("/auth/discord", authH.Start)
	r.Get("/auth/discord/callback", authH.Callback)
	r.Post("/logout", authH.Logout)

	// authed, no slug required (onboarding itself)
	r.Group(func(r chi.Router) {
		r.Use(authH.RequireSession)
		r.Get("/onboarding", hs.OnboardingGet)
		r.Post("/onboarding", hs.OnboardingPost)
	})

	// authed + slug claimed
	r.Group(func(r chi.Router) {
		r.Use(authH.RequireSlug)
		r.Get("/dashboard", hs.Dashboard)
	})

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
