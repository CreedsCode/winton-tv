// Package auth implements the Discord OAuth2 flow with guild membership gate.
//
// Flow:
//   1. GET /auth/discord            → generate state nonce, store in session,
//                                     redirect to Discord authorize URL.
//   2. GET /auth/discord/callback   → verify state, exchange code for token,
//                                     fetch /users/@me and /users/@me/guilds,
//                                     reject if user is not in DISCORD_GUILD_ID,
//                                     upsert user, set session user_id,
//                                     redirect to /onboarding or /dashboard.
//   3. POST /logout                 → destroy session, redirect to /.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/CreedsCode/winton-tv/internal/config"
	"github.com/CreedsCode/winton-tv/internal/store"
	"github.com/alexedwards/scs/v2"
	"golang.org/x/oauth2"
)

const (
	sessionKeyUserID     = "user_id"
	sessionKeyOAuthState = "oauth_state"
)

type Auth struct {
	cfg     *config.Config
	store   *store.Store
	session *scs.SessionManager
	oauth   *oauth2.Config
	logger  *slog.Logger
}

func New(cfg *config.Config, st *store.Store, session *scs.SessionManager, logger *slog.Logger) *Auth {
	return &Auth{
		cfg:     cfg,
		store:   st,
		session: session,
		logger:  logger,
		oauth: &oauth2.Config{
			ClientID:     cfg.DiscordClientID,
			ClientSecret: cfg.DiscordClientSecret,
			RedirectURL:  cfg.DiscordCallbackURL(),
			Scopes:       []string{"identify", "guilds"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  "https://discord.com/api/oauth2/authorize",
				TokenURL: "https://discord.com/api/oauth2/token",
			},
		},
	}
}

// Start kicks off OAuth. Stashes a state nonce in the session, sends
// the user to Discord.
func (a *Auth) Start(w http.ResponseWriter, r *http.Request) {
	state, err := nonce()
	if err != nil {
		a.logger.Error("oauth state nonce", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	a.session.Put(r.Context(), sessionKeyOAuthState, state)
	http.Redirect(w, r, a.oauth.AuthCodeURL(state), http.StatusFound)
}

// Callback completes the OAuth flow.
func (a *Auth) Callback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	gotState := r.URL.Query().Get("state")
	wantState := a.session.GetString(ctx, sessionKeyOAuthState)
	a.session.Remove(ctx, sessionKeyOAuthState)
	if gotState == "" || gotState != wantState {
		a.logger.Warn("oauth state mismatch", "got", gotState != "", "want", wantState != "")
		http.Error(w, "invalid oauth state", http.StatusBadRequest)
		return
	}

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		a.logger.Info("oauth user-cancelled", "error", errParam)
		http.Redirect(w, r, "/login?denied=1", http.StatusFound)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	token, err := a.oauth.Exchange(ctx, code)
	if err != nil {
		a.logger.Warn("oauth exchange", "err", err)
		http.Error(w, "exchange failed", http.StatusBadRequest)
		return
	}

	client := a.oauth.Client(ctx, token)

	me, err := fetchMe(ctx, client)
	if err != nil {
		a.logger.Warn("fetch me", "err", err)
		http.Error(w, "failed to fetch Discord profile", http.StatusBadGateway)
		return
	}

	guilds, err := fetchGuilds(ctx, client)
	if err != nil {
		a.logger.Warn("fetch guilds", "err", err)
		http.Error(w, "failed to fetch Discord guilds", http.StatusBadGateway)
		return
	}

	if !inGuild(guilds, a.cfg.DiscordGuildID) {
		a.logger.Info("guild gate denied", "discord_id", me.ID)
		http.Error(w,
			"you are not a member of the Winton Discord — join the server and try again",
			http.StatusForbidden)
		return
	}

	u, err := a.store.UpsertUser(ctx, store.User{
		DiscordID:  me.ID,
		Username:   me.Username,
		GlobalName: deref(me.GlobalName, me.Username),
		AvatarHash: me.Avatar,
	})
	if err != nil {
		a.logger.Error("upsert user", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if u.Banned {
		http.Error(w, "your account is banned", http.StatusForbidden)
		return
	}

	// Auto-promote admins listed in ADMIN_DISCORD_IDS env. Runs every
	// login so an admin can demote themselves and re-login to re-promote
	// (mostly used to bootstrap the very first admin).
	if a.cfg.AdminDiscordIDs[u.DiscordID] && !u.IsAdmin {
		if err := a.store.PromoteAdmin(ctx, u.ID); err != nil {
			a.logger.Warn("auto-promote admin", "uid", u.ID, "err", err)
		} else {
			a.logger.Info("auto-promoted admin from ADMIN_DISCORD_IDS", "uid", u.ID, "discord_id", u.DiscordID)
			u.IsAdmin = true
		}
	}

	// Renew the session ID on auth — prevents session fixation.
	if err := a.session.RenewToken(ctx); err != nil {
		a.logger.Warn("renew token", "err", err)
	}
	a.session.Put(ctx, sessionKeyUserID, u.ID)

	if u.Slug == nil || *u.Slug == "" {
		http.Redirect(w, r, "/onboarding", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusFound)
}

// Logout clears the session.
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	if err := a.session.Destroy(r.Context()); err != nil {
		a.logger.Warn("session destroy", "err", err)
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

// ─────────────────────── Discord API ───────────────────────

type discordMe struct {
	ID         string  `json:"id"`
	Username   string  `json:"username"`
	GlobalName *string `json:"global_name"`
	Avatar     *string `json:"avatar"`
}

type discordGuild struct {
	ID string `json:"id"`
}

func fetchMe(ctx context.Context, c *http.Client) (*discordMe, error) {
	var out discordMe
	if err := getJSON(ctx, c, "https://discord.com/api/users/@me", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func fetchGuilds(ctx context.Context, c *http.Client) ([]discordGuild, error) {
	var out []discordGuild
	if err := getJSON(ctx, c, "https://discord.com/api/users/@me/guilds", &out); err != nil {
		return nil, err
	}
	return out, nil
}

func getJSON(ctx context.Context, c *http.Client, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("%s -> %d: %s", url, resp.StatusCode, string(body))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func inGuild(guilds []discordGuild, target string) bool {
	for _, g := range guilds {
		if g.ID == target {
			return true
		}
	}
	return false
}

func deref(p *string, fallback string) string {
	if p == nil || *p == "" {
		return fallback
	}
	return *p
}

func nonce() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
