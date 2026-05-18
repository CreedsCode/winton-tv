// Package store owns the Postgres connection pool and all SQL.
//
// Migrations live in ./migrations/*.sql, are embedded into the binary,
// and run at startup. Schema is tracked in a `schema_migrations` table.
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Store struct {
	pool *pgxpool.Pool
}

func New(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}

	s := &Store{pool: pool}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Pool() *pgxpool.Pool { return s.pool }
func (s *Store) Close()              { s.pool.Close() }

// ─────────────────────── users ───────────────────────

type User struct {
	ID         int64
	DiscordID  string
	Username   string
	GlobalName string
	AvatarHash *string
	Slug       *string
	IsAdmin    bool
	Banned     bool
	CreatedAt  time.Time

	// Stream credentials (V1.3) — null until first /dashboard/setup-stream click.
	IngressID       *string
	StreamKey       *string
	StreamWhipURL   *string
	StreamCreatedAt *time.Time

	// Channel discovery setting. One of "public", "members", "unlisted".
	// Default "public" via migration. Controls whether the channel appears
	// on the discovery grid; the channel page itself is always accessible.
	Discovery string

	// Stream metadata (V1.5) — both default '' via migration.
	Title       string
	Description string

	// V1.8: richer profile + ban audit context
	Bio           string  // profile bio (plain text, line breaks preserved on render)
	SocialLinks   string  // JSON: {twitter, twitch, youtube, web} — empty string = none
	BannedAt      *time.Time
	BannedByID    *int64
	BannedReason  string
}

// Discovery values. Mirror the CHECK constraint in migration 004.
const (
	DiscoveryPublic   = "public"
	DiscoveryMembers  = "members"
	DiscoveryUnlisted = "unlisted"
)

const userColumns = `
    id, discord_id, username, global_name, avatar_hash, slug,
    is_admin, banned, created_at,
    ingress_id, stream_key, stream_whip_url, stream_created_at,
    discovery,
    title, description,
    bio, social_links, banned_at, banned_by_id, banned_reason
`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	if err := row.Scan(
		&u.ID, &u.DiscordID, &u.Username, &u.GlobalName, &u.AvatarHash, &u.Slug,
		&u.IsAdmin, &u.Banned, &u.CreatedAt,
		&u.IngressID, &u.StreamKey, &u.StreamWhipURL, &u.StreamCreatedAt,
		&u.Discovery,
		&u.Title, &u.Description,
		&u.Bio, &u.SocialLinks, &u.BannedAt, &u.BannedByID, &u.BannedReason,
	); err != nil {
		return nil, err
	}
	return &u, nil
}

// UpsertUser inserts or updates a user keyed by discord_id.
func (s *Store) UpsertUser(ctx context.Context, u User) (*User, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO users (discord_id, username, global_name, avatar_hash, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (discord_id) DO UPDATE
		   SET username    = EXCLUDED.username,
		       global_name = EXCLUDED.global_name,
		       avatar_hash = EXCLUDED.avatar_hash,
		       updated_at  = now()
		RETURNING `+userColumns,
		u.DiscordID, u.Username, u.GlobalName, u.AvatarHash,
	)
	return scanUser(row)
}

func (s *Store) GetUserByID(ctx context.Context, id int64) (*User, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = $1`, id,
	)
	u, err := scanUser(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return u, nil
}

func (s *Store) GetUserBySlug(ctx context.Context, slug string) (*User, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE lower(slug) = lower($1)`, slug,
	)
	u, err := scanUser(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return u, nil
}

// GetUserByDiscordID looks up a user by their Discord snowflake. Used
// to map Discord voice-channel members to winton-tv accounts for the
// /c/<voice-channel> multi-view.
func (s *Store) GetUserByDiscordID(ctx context.Context, discordID string) (*User, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE discord_id = $1`, discordID,
	)
	u, err := scanUser(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return u, nil
}

func (s *Store) SlugTaken(ctx context.Context, slug string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM users WHERE lower(slug) = lower($1))`,
		slug,
	).Scan(&exists)
	return exists, err
}

func (s *Store) SetUserSlug(ctx context.Context, userID int64, slug string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE users SET slug = $1, updated_at = now() WHERE id = $2 AND slug IS NULL`,
		slug, userID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("slug already set or user missing")
	}
	return nil
}

// ─────────────────────── profile + admin (V1.8) ───────────────────────

func (s *Store) SetUserProfile(ctx context.Context, userID int64, bio, socialLinks string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE users SET bio = $1, social_links = $2, updated_at = now() WHERE id = $3
	`, bio, socialLinks, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("user not found")
	}
	return nil
}

// PromoteAdmin sets is_admin=true. Idempotent.
func (s *Store) PromoteAdmin(ctx context.Context, userID int64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET is_admin = true, updated_at = now() WHERE id = $1`, userID)
	return err
}

// DemoteAdmin sets is_admin=false. Idempotent.
func (s *Store) DemoteAdmin(ctx context.Context, userID int64) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE users SET is_admin = false, updated_at = now() WHERE id = $1`, userID)
	return err
}

// BanUser sets banned=true plus audit context. Reason can be empty.
func (s *Store) BanUser(ctx context.Context, userID, adminID int64, reason string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE users
		   SET banned = true,
		       banned_at = now(),
		       banned_by_id = $1,
		       banned_reason = $2,
		       updated_at = now()
		 WHERE id = $3
	`, adminID, reason, userID)
	return err
}

func (s *Store) UnbanUser(ctx context.Context, userID int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE users
		   SET banned = false,
		       banned_at = NULL,
		       banned_by_id = NULL,
		       banned_reason = '',
		       updated_at = now()
		 WHERE id = $1
	`, userID)
	return err
}

// ListUsersFilter narrows ListUsers results.
type ListUsersFilter struct {
	Query    string // case-insensitive match against slug, global_name, username
	OnlyAdmin   bool
	OnlyBanned  bool
	Limit       int  // default 100
}

func (s *Store) ListUsers(ctx context.Context, f ListUsersFilter) ([]*User, error) {
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	conds := []string{"1=1"}
	args := []any{}
	if f.Query != "" {
		args = append(args, "%"+strings.ToLower(f.Query)+"%")
		conds = append(conds, fmt.Sprintf(
			"(lower(slug) LIKE $%d OR lower(global_name) LIKE $%d OR lower(username) LIKE $%d)",
			len(args), len(args), len(args),
		))
	}
	if f.OnlyAdmin {
		conds = append(conds, "is_admin = true")
	}
	if f.OnlyBanned {
		conds = append(conds, "banned = true")
	}
	args = append(args, limit)

	q := `SELECT ` + userColumns + ` FROM users WHERE ` +
		strings.Join(conds, " AND ") +
		` ORDER BY created_at DESC LIMIT $` + fmt.Sprintf("%d", len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// AdminAction is a single audit log row.
type AdminAction struct {
	ID           int64
	AdminID      int64
	AdminName    string  // joined from users
	TargetUserID *int64
	TargetName   *string // joined from users (nullable)
	TargetSlug   *string
	Action       string
	Reason       string
	CreatedAt    time.Time
}

// Action constants. Keep in sync with handler dispatcher.
const (
	ActionBan          = "BAN"
	ActionUnban        = "UNBAN"
	ActionKickStream   = "KICK_STREAM"
	ActionPromoteAdmin = "PROMOTE_ADMIN"
	ActionDemoteAdmin  = "DEMOTE_ADMIN"
)

func (s *Store) LogAdminAction(ctx context.Context, a AdminAction) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO admin_actions (admin_id, target_user_id, target_slug, action, reason)
		VALUES ($1, $2, $3, $4, $5)
	`, a.AdminID, a.TargetUserID, a.TargetSlug, a.Action, a.Reason)
	return err
}

func (s *Store) ListAdminActions(ctx context.Context, limit int) ([]*AdminAction, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.admin_id, ad.global_name,
		       a.target_user_id, tu.global_name,
		       a.target_slug, a.action, a.reason, a.created_at
		  FROM admin_actions a
		  JOIN users ad ON ad.id = a.admin_id
		  LEFT JOIN users tu ON tu.id = a.target_user_id
		 ORDER BY a.created_at DESC
		 LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AdminAction{}
	for rows.Next() {
		var a AdminAction
		if err := rows.Scan(
			&a.ID, &a.AdminID, &a.AdminName,
			&a.TargetUserID, &a.TargetName,
			&a.TargetSlug, &a.Action, &a.Reason, &a.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

// ─────────────────────── stream credentials (V1.3) ───────────────────────

func (s *Store) SetStreamCredentials(ctx context.Context, userID int64, ingressID, streamKey, whipURL string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE users
		   SET ingress_id        = $1,
		       stream_key        = $2,
		       stream_whip_url   = $3,
		       stream_created_at = now(),
		       updated_at        = now()
		 WHERE id = $4
	`, ingressID, streamKey, whipURL, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("user not found")
	}
	return nil
}

// SetUserMetadata updates title + description. Caller validates lengths.
func (s *Store) SetUserMetadata(ctx context.Context, userID int64, title, description string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE users SET title = $1, description = $2, updated_at = now() WHERE id = $3
	`, title, description, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("user not found")
	}
	return nil
}

// SetUserDiscovery updates the user's discovery setting. Caller must
// validate `value` is one of the Discovery* constants — DB CHECK rejects
// anything else but the error is opaque.
func (s *Store) SetUserDiscovery(ctx context.Context, userID int64, value string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE users SET discovery = $1, updated_at = now() WHERE id = $2`,
		value, userID,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("user not found")
	}
	return nil
}

func (s *Store) ClearStreamCredentials(ctx context.Context, userID int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE users
		   SET ingress_id        = NULL,
		       stream_key        = NULL,
		       stream_whip_url   = NULL,
		       stream_created_at = NULL,
		       updated_at        = now()
		 WHERE id = $1
	`, userID)
	return err
}

// ─────────────────────── chat (V1.8.1: persistence) ───────────────────────

type ChatMessage struct {
	ID              int64     `json:"id"`
	ChannelSlug     string    `json:"channel_slug"`
	SenderUserID    int64     `json:"sender_user_id"`
	SenderName      string    `json:"sender_name"`
	SenderAvatarURL string    `json:"sender_avatar_url"`
	SenderSlug      string    `json:"sender_slug"`
	IsOwner         bool      `json:"is_owner"`
	Text            string    `json:"text"`
	CreatedAt       time.Time `json:"created_at"`
}

func (s *Store) InsertChatMessage(ctx context.Context, m ChatMessage) (*ChatMessage, error) {
	row := s.pool.QueryRow(ctx, `
		INSERT INTO chat_messages
		    (channel_slug, sender_user_id, sender_name, sender_avatar_url, sender_slug, is_owner, text)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at
	`, m.ChannelSlug, m.SenderUserID, m.SenderName, m.SenderAvatarURL, m.SenderSlug, m.IsOwner, m.Text)
	if err := row.Scan(&m.ID, &m.CreatedAt); err != nil {
		return nil, err
	}
	return &m, nil
}

// RecentChatMessages returns the last `limit` messages for a channel,
// oldest-first (display order).
func (s *Store) RecentChatMessages(ctx context.Context, channelSlug string, limit int) ([]*ChatMessage, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, channel_slug, sender_user_id, sender_name, sender_avatar_url,
		       sender_slug, is_owner, text, created_at
		  FROM chat_messages
		 WHERE channel_slug = $1
		 ORDER BY created_at DESC
		 LIMIT $2
	`, channelSlug, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ChatMessage{}
	for rows.Next() {
		var m ChatMessage
		if err := rows.Scan(
			&m.ID, &m.ChannelSlug, &m.SenderUserID, &m.SenderName, &m.SenderAvatarURL,
			&m.SenderSlug, &m.IsOwner, &m.Text, &m.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	// Reverse to oldest-first for display.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, rows.Err()
}

// ─────────────────────── migrations ───────────────────────

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
		    version    TEXT PRIMARY KEY,
		    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var applied bool
		if err := s.pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`,
			name,
		).Scan(&applied); err != nil {
			return fmt.Errorf("check %s: %w", name, err)
		}
		if applied {
			continue
		}

		raw, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}

		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, string(raw)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version) VALUES ($1)`, name,
		); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("record %s: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit %s: %w", name, err)
		}
	}
	return nil
}
