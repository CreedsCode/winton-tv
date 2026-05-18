// Package handlers wires HTTP handlers and HTML templates.
package handlers

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/CreedsCode/winton-tv/internal/auth"
	"github.com/CreedsCode/winton-tv/internal/config"
	"github.com/CreedsCode/winton-tv/internal/livekit"
	"github.com/CreedsCode/winton-tv/internal/store"
	"github.com/go-chi/chi/v5"
)

// chatMetadata is what gets stamped into a viewer's LiveKit participant
// metadata. Other clients deserialise this when rendering chat lines so
// the sender's avatar + Discord identity are server-attested (came from
// our JWT signing, not from the chat payload itself).
type chatMetadata struct {
	AvatarURL string `json:"avatar_url,omitempty"`
	DiscordID string `json:"discord_id,omitempty"`
}

type Handler struct {
	cfg     *config.Config
	store   *store.Store
	livekit *livekit.Client
	logger  *slog.Logger
	tmpl    *template.Template
}

func New(cfg *config.Config, st *store.Store, lk *livekit.Client, logger *slog.Logger) (*Handler, error) {
	tmpl, err := template.ParseGlob("web/templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	return &Handler{cfg: cfg, store: st, livekit: lk, logger: logger, tmpl: tmpl}, nil
}

// ─────────────────────── public pages ───────────────────────

// LiveCard is the data shape the landing page template iterates over.
type LiveCard struct {
	Slug        string
	DisplayName string
	ViewerCount int
}

func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	cards, err := h.liveCards(r)
	if err != nil {
		h.logger.Warn("index live cards", "err", err)
	}
	h.render(w, "index.html", map[string]any{
		"User":  auth.Current(r),
		"Cards": cards,
	})
}

func (h *Handler) liveCards(r *http.Request) ([]LiveCard, error) {
	streams, err := h.livekit.ListLive(r.Context())
	if err != nil {
		return nil, err
	}
	viewer := auth.Current(r) // nil = anonymous
	cards := make([]LiveCard, 0, len(streams))
	for _, s := range streams {
		user, err := h.store.GetUserBySlug(r.Context(), s.Slug)
		if err != nil {
			h.logger.Warn("liveCards: get user by slug", "slug", s.Slug, "err", err)
			continue
		}
		if user == nil {
			continue // room exists but no matching user record (orphan)
		}
		if !shouldShowInDiscovery(user.Discovery, viewer != nil) {
			continue
		}
		cards = append(cards, LiveCard{
			Slug:        s.Slug,
			DisplayName: user.GlobalName,
			ViewerCount: s.ViewerCount,
		})
	}
	return cards, nil
}

// shouldShowInDiscovery applies the channel's discovery setting against
// the viewer's auth state. Authenticated viewers in our DB are by
// construction Winton guild members (the auth.Callback gates on that).
func shouldShowInDiscovery(setting string, viewerAuthed bool) bool {
	switch setting {
	case store.DiscoveryPublic:
		return true
	case store.DiscoveryMembers:
		return viewerAuthed
	case store.DiscoveryUnlisted:
		return false
	default:
		// Unknown value — fail closed (don't surface accidentally).
		return false
	}
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
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

// ─────────────────────── channel (public viewer page) ───────────────────────

// Channel resolves a slug -> user, mints a viewer JWT, and renders the
// watch page. Anonymous viewers are allowed (V1 requirement). Identity
// is a random "guest-xxxxx" so LiveKit can tell viewers apart.
func (h *Handler) Channel(w http.ResponseWriter, r *http.Request) {
	slug := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "slug")))

	// Defensive — chi shouldn't route reserved slugs here (they're earlier
	// in the trie), but guard anyway.
	if slug == "" || reservedSlugs[slug] {
		http.NotFound(w, r)
		return
	}

	user, err := h.store.GetUserBySlug(r.Context(), slug)
	if err != nil {
		h.logger.Error("channel: get user by slug", "slug", slug, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if user == nil {
		w.WriteHeader(http.StatusNotFound)
		h.render(w, "channel_404.html", map[string]any{"Slug": slug})
		return
	}

	viewerIdentity, err := guestIdentity()
	if err != nil {
		h.logger.Error("guest identity", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	viewer := auth.Current(r)
	opts := livekit.ViewerOptions{
		Identity:    viewerIdentity,
		Room:        *user.Slug,
		TTL:         6 * time.Hour,
		DisplayName: "Guest",
		CanChat:     false,
	}
	if viewer != nil {
		opts.DisplayName = viewer.GlobalName
		opts.CanChat = true
		meta := chatMetadata{
			AvatarURL: discordAvatarURL(viewer),
			DiscordID: viewer.DiscordID,
		}
		if b, err := json.Marshal(meta); err == nil {
			opts.Metadata = string(b)
		}
	}

	token, err := h.livekit.ViewerToken(opts)
	if err != nil {
		h.logger.Error("viewer token", "slug", slug, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	live, err := h.livekit.IsLive(r.Context(), *user.Slug)
	if err != nil {
		h.logger.Warn("channel live check", "slug", slug, "err", err)
	}

	h.render(w, "channel.html", map[string]any{
		"Channel":          user,
		"Viewer":           viewer, // may be nil (anonymous)
		"LiveKitToken":     token,
		"LiveKitPublicURL": h.livekit.PublicURL(),
		"Live":             live,
		"CanChat":          viewer != nil,
	})
}

// discordAvatarURL returns the user's Discord CDN avatar URL or "" if
// they don't have a custom avatar set. Discord IDs and avatar hashes
// are public so no leak concern.
func discordAvatarURL(u *store.User) string {
	if u == nil || u.AvatarHash == nil || *u.AvatarHash == "" {
		return ""
	}
	return "https://cdn.discordapp.com/avatars/" + u.DiscordID + "/" + *u.AvatarHash + ".png?size=64"
}

func guestIdentity() (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "guest-" + base64.RawURLEncoding.EncodeToString(b), nil
}

// ─────────────────────── dashboard ───────────────────────

func (h *Handler) Dashboard(w http.ResponseWriter, r *http.Request) {
	u := auth.Current(r)
	live, err := h.livekit.IsLive(r.Context(), *u.Slug)
	if err != nil {
		h.logger.Warn("live check (dashboard render)", "slug", *u.Slug, "err", err)
	}
	h.render(w, "dashboard.html", map[string]any{
		"User": u,
		"Live": live,
	})
}

// DashboardSetupStream creates a WHIP ingress for the user if they don't
// have one. Idempotent — second click after creation just redirects.
func (h *Handler) DashboardSetupStream(w http.ResponseWriter, r *http.Request) {
	u := auth.Current(r)
	if u.IngressID != nil && *u.IngressID != "" {
		http.Redirect(w, r, "/dashboard", http.StatusFound)
		return
	}
	if err := h.createAndPersistIngress(r, u); err != nil {
		h.logger.Error("setup stream", "uid", u.ID, "err", err)
		http.Error(w, "failed to create stream (LiveKit Ingress unreachable?) — try again", http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

// DashboardRotateStream destroys the current ingress and provisions a new
// one. Used when a key leaks or for routine hygiene.
func (h *Handler) DashboardRotateStream(w http.ResponseWriter, r *http.Request) {
	u := auth.Current(r)
	if u.IngressID != nil && *u.IngressID != "" {
		if err := h.livekit.DeleteIngress(r.Context(), *u.IngressID); err != nil {
			h.logger.Warn("delete ingress on rotate", "id", *u.IngressID, "err", err)
		}
	}
	if err := h.store.ClearStreamCredentials(r.Context(), u.ID); err != nil {
		h.logger.Error("clear stream creds", "uid", u.ID, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Re-create (need to reload user so IngressID is now nil)
	u.IngressID = nil
	if err := h.createAndPersistIngress(r, u); err != nil {
		h.logger.Error("rotate -> create", "uid", u.ID, "err", err)
		http.Error(w, "failed to create new stream — try again", http.StatusBadGateway)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

// DashboardSetDiscovery updates the user's channel discovery setting.
// Posted from the dashboard form. HTMX-friendly: returns 204 No Content
// on success so HTMX doesn't swap anything.
func (h *Handler) DashboardSetDiscovery(w http.ResponseWriter, r *http.Request) {
	u := auth.Current(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	value := r.FormValue("discovery")
	switch value {
	case store.DiscoveryPublic, store.DiscoveryMembers, store.DiscoveryUnlisted:
		// ok
	default:
		http.Error(w, "invalid discovery value", http.StatusBadRequest)
		return
	}
	if err := h.store.SetUserDiscovery(r.Context(), u.ID, value); err != nil {
		h.logger.Error("set discovery", "uid", u.ID, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Non-HTMX: redirect back. HTMX (hx-trigger=change): 204 no-content,
	// browser keeps the radio user just picked.
	if r.Header.Get("HX-Request") == "true" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

// DashboardLive returns just the live badge — polled by HTMX every 10s.
func (h *Handler) DashboardLive(w http.ResponseWriter, r *http.Request) {
	u := auth.Current(r)
	live, err := h.livekit.IsLive(r.Context(), *u.Slug)
	if err != nil {
		h.logger.Warn("live check (poll)", "slug", *u.Slug, "err", err)
	}
	h.render(w, "_live_badge.html", map[string]any{"Live": live})
}

func (h *Handler) createAndPersistIngress(r *http.Request, u *store.User) error {
	creds, err := h.livekit.CreateWHIPIngress(r.Context(), *u.Slug)
	if err != nil {
		return fmt.Errorf("create ingress: %w", err)
	}
	if err := h.store.SetStreamCredentials(r.Context(), u.ID, creds.IngressID, creds.StreamKey, creds.WhipURL); err != nil {
		// Orphan ingress cleanup, best-effort
		_ = h.livekit.DeleteIngress(r.Context(), creds.IngressID)
		return fmt.Errorf("persist creds: %w", err)
	}
	return nil
}

// ─────────────────────── onboarding (slug picker) ───────────────────────

var (
	slugRe        = regexp.MustCompile(`^[a-z0-9_-]{3,32}$`)
	reservedSlugs = mustReservedSet()
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
