package auth

import (
	"context"
	"net/http"

	"github.com/CreedsCode/winton-tv/internal/store"
)

type ctxKey int

const userCtxKey ctxKey = iota

// LoadUser is mounted globally. If the session has a user_id, the user is
// loaded into the request context. Never blocks the request — the handler
// can read Current(r) and decide what to do.
func (a *Auth) LoadUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid := a.session.GetInt64(r.Context(), sessionKeyUserID)
		if uid == 0 {
			next.ServeHTTP(w, r)
			return
		}
		u, err := a.store.GetUserByID(r.Context(), uid)
		if err != nil {
			a.logger.Warn("load user", "uid", uid, "err", err)
			next.ServeHTTP(w, r)
			return
		}
		if u == nil || u.Banned {
			_ = a.session.Destroy(r.Context())
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(withUser(r.Context(), u)))
	})
}

// RequireSession redirects to /login when no user in context.
func (a *Auth) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if Current(r) == nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireSlug requires both an authed session AND a chosen slug.
// Sends slug-less users to /onboarding.
func (a *Auth) RequireSlug(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := Current(r)
		if u == nil {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		if u.Slug == nil || *u.Slug == "" {
			http.Redirect(w, r, "/onboarding", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func withUser(ctx context.Context, u *store.User) context.Context {
	return context.WithValue(ctx, userCtxKey, u)
}

// Current returns the authed user from request context, or nil.
func Current(r *http.Request) *store.User {
	if v, ok := r.Context().Value(userCtxKey).(*store.User); ok {
		return v
	}
	return nil
}
