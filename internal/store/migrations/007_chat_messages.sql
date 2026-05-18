-- Persistent chat. Replaces ephemeral LiveKit DataChannel chat.
-- Each row is a single message; sender_* fields are snapshotted at send
-- time so renaming/avatar-change doesn't rewrite history.

CREATE TABLE chat_messages (
    id                BIGSERIAL    PRIMARY KEY,
    channel_slug      TEXT         NOT NULL,
    sender_user_id    BIGINT       NOT NULL REFERENCES users(id),
    sender_name       TEXT         NOT NULL,
    sender_avatar_url TEXT         NOT NULL DEFAULT '',
    sender_slug       TEXT         NOT NULL DEFAULT '',
    is_owner          BOOLEAN      NOT NULL DEFAULT false,
    text              TEXT         NOT NULL,
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- Lookup pattern: most recent N messages for a channel.
CREATE INDEX idx_chat_messages_channel_created
    ON chat_messages (channel_slug, created_at DESC);
