// Package session wires up scs (server-side sessions) backed by Postgres.
//
// Sessions store opaque IDs in cookies; all session data lives in the
// sessions table that postgresstore.New creates on first use.
package session

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/alexedwards/scs/postgresstore"
	"github.com/alexedwards/scs/v2"
)

// New returns a session manager. baseURL drives Cookie.Secure (HTTPS-only
// cookies in prod, but still functional on plain-HTTP localhost dev).
func New(db *sql.DB, baseURL string) *scs.SessionManager {
	mgr := scs.New()
	mgr.Store = postgresstore.New(db)
	mgr.Lifetime = 30 * 24 * time.Hour
	mgr.IdleTimeout = 7 * 24 * time.Hour

	mgr.Cookie.Name = "winton_session"
	mgr.Cookie.Path = "/"
	mgr.Cookie.HttpOnly = true
	mgr.Cookie.Secure = strings.HasPrefix(baseURL, "https://")
	mgr.Cookie.SameSite = http.SameSiteLaxMode // Lax = OAuth callback works

	return mgr
}
