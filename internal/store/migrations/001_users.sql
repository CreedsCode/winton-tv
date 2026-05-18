CREATE TABLE users (
    id          BIGSERIAL PRIMARY KEY,
    discord_id  TEXT        NOT NULL UNIQUE,
    username    TEXT        NOT NULL,          -- discord username (handle, lowercase)
    global_name TEXT        NOT NULL,          -- discord display name
    avatar_hash TEXT,                          -- nullable; null = default avatar
    slug        TEXT        UNIQUE,            -- nullable until user picks one in onboarding
    is_admin    BOOLEAN     NOT NULL DEFAULT false,
    banned      BOOLEAN     NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Case-insensitive uniqueness on slug (Discord usernames are already case-folded but
-- channel slugs need explicit protection so "Maya" and "maya" can't both exist).
CREATE UNIQUE INDEX idx_users_slug_lower ON users (lower(slug)) WHERE slug IS NOT NULL;
