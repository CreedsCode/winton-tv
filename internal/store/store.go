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
    title, description
`

func scanUser(row pgx.Row) (*User, error) {
	var u User
	if err := row.Scan(
		&u.ID, &u.DiscordID, &u.Username, &u.GlobalName, &u.AvatarHash, &u.Slug,
		&u.IsAdmin, &u.Banned, &u.CreatedAt,
		&u.IngressID, &u.StreamKey, &u.StreamWhipURL, &u.StreamCreatedAt,
		&u.Discovery,
		&u.Title, &u.Description,
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
