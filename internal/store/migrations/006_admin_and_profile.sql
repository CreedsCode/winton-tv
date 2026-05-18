-- V1.8: admin moderation + richer user profiles.
--
-- - Profile fields: bio (plain text, line-break-preserving like description) +
--   social_links (JSON {twitter, twitch, youtube, web}).
-- - Ban context: who banned, when, why. Existing banned BOOL stays (still
--   the gate); these add audit-trail data.
-- - admin_actions: full audit log of every moderation action. Survives
--   user deletion (target_user_id is nullable, target_slug preserves the
--   original target reference).

ALTER TABLE users
    ADD COLUMN bio              TEXT        NOT NULL DEFAULT '',
    ADD COLUMN social_links     TEXT        NOT NULL DEFAULT '',
    ADD COLUMN banned_at        TIMESTAMPTZ,
    ADD COLUMN banned_by_id     BIGINT      REFERENCES users(id),
    ADD COLUMN banned_reason    TEXT        NOT NULL DEFAULT '';

CREATE TABLE admin_actions (
    id              BIGSERIAL    PRIMARY KEY,
    admin_id        BIGINT       NOT NULL REFERENCES users(id),
    target_user_id  BIGINT       REFERENCES users(id),
    target_slug     TEXT,
    action          TEXT         NOT NULL,
    reason          TEXT         NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE INDEX idx_admin_actions_created ON admin_actions (created_at DESC);
CREATE INDEX idx_admin_actions_target  ON admin_actions (target_user_id) WHERE target_user_id IS NOT NULL;
