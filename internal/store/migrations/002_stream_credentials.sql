-- Per-user LiveKit Ingress credentials. One ingress per user, created
-- on-demand from the dashboard. "Rotate" deletes the row + the upstream
-- ingress and re-creates.

ALTER TABLE users
    ADD COLUMN ingress_id            TEXT,
    ADD COLUMN stream_key            TEXT,
    ADD COLUMN stream_whip_url       TEXT,
    ADD COLUMN stream_created_at     TIMESTAMPTZ;

-- stream_key must be unique across users (it's effectively a bearer token).
-- Nullable until first creation.
CREATE UNIQUE INDEX idx_users_stream_key ON users (stream_key) WHERE stream_key IS NOT NULL;
CREATE UNIQUE INDEX idx_users_ingress_id ON users (ingress_id) WHERE ingress_id IS NOT NULL;
