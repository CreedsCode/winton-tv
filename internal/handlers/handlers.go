// Package handlers wires HTTP handlers and HTML templates.
package handlers

import (
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/CreedsCode/winton-tv/internal/auth"
	"github.com/CreedsCode/winton-tv/internal/config"
	"github.com/CreedsCode/winton-tv/internal/store"
)

type Handler struct {
	cfg    *config.Config
	store  *store.Store
	logger *slog.Logger
	tmpl   *template.Template
}

func New(cfg *config.Config, st *store.Store, logger *slog.Logger) (*Handler, error) {
	tmpl, err := template.ParseGlob("web/templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return &Handler{cfg: cfg, store: st, logger: logger, tmpl: tmpl}, nil
}

// ─────────────────────── public pages ───────────────────────

func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	h.render(w, "index.html", map[string]any{
		"User": auth.Current(r),
	})
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	// If already authed, skip the login page.
	if u := auth.Current(r); u != nil {
		dest := "/dashboard"
		if u.Slug == nil || *u.Slug == "" {
			dest = "/onboarding"
		}
		http.Redirect(w, r, dest, http.StatusFound)
		return
	}
	h.render(w, "login.html", map[string]any{
		"Denied": r.URL.Query().Get("denied") == "1",
	})
}

func (h *Handler) Healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// ─────────────────────── authed pages ───────────────────────

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	h.render(w, "dashboard.html", map[string]any{
		"User": auth.Current(r),
	})
}

// ─────────────────────── onboarding (slug picker) ───────────────────────

var (
	slugRe         = regexp.MustCompile(`^[a-z0-9_-]{3,32}$`)
	reservedSlugs  = mustReservedSet()
)

func (h *Handler) OnboardingGet(w http.ResponseWriter, r *http.Request) {
	u := auth.Current(r)
	if u.Slug != nil && *u.Slug != "" {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}
	h.render(w, "onboarding.html", map[string]any{
		"User":      u,
		"Attempted": "",
		"Error":     "",
	})
}

func (h *Handler) OnboardingPost(w http.ResponseWriter, r *http.Request) {
	u := auth.Current(r)
	if u.Slug != nil && *u.Slug != "" {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	slug := strings.ToLower(strings.TrimSpace(r.FormValue("slug")))

	if !slugRe.MatchString(slug) {
		h.onboardingError(w, r, u, slug, "Slug must be 3–32 chars: lowercase letters, numbers, _ and -.")
		return
	}
	if reservedSlugs[slug] {
		h.onboardingError(w, r, u, slug, "That slug is reserved. Pick another.")
		return
	}

	taken, err := h.store.SlugTaken(r.Context(), slug)
	if err != nil {
		h.logger.Error("slug check", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if taken {
		h.onboardingError(w, r, u, slug, "That slug is taken. Pick another.")
		return
	}

	if err := h.store.SetUserSlug(r.Context(), u.ID, slug); err != nil {
		h.logger.Error("set slug", "uid", u.ID, "slug", slug, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

func (h *Handler) onboardingError(w http.ResponseWriter, r *http.Request, u *store.User, attempted, msg string) {
	w.WriteHeader(http.StatusUnprocessableEntity)
	h.render(w, "onboarding.html", map[string]any{
		"User":      u,
		"Attempted": attempted,
		"Error":     msg,
	})
}

// ─────────────────────── helpers ───────────────────────

func (h *Handler) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		h.logger.Error("template render", "name", name, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

func mustReservedSet() map[string]bool {
	// Reserved at the routing layer (existing routes) and for future expansion.
	// Keep this list in sync with new top-level routes.
	list := []string{
		"about", "admin", "api", "auth", "callback", "channel", "channels",
		"chat", "dashboard", "discord", "docs", "healthz", "help", "login",
		"logout", "onboarding", "privacy", "search", "settings", "static",
		"stream", "streams", "support", "tos", "watch",
	}
	out := make(map[string]bool, len(list))
	for _, s := range list {
		out[s] = true
	}
	return out
}
