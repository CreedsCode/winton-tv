-- Per-user discovery setting controls whether the channel shows up on
-- the public landing-page grid. Channel page (/<slug>) always works
-- regardless — this only gates the discovery surface.
--
--   'public'   — anyone (logged in or not) sees the card
--   'members'  — only authenticated Winton guild members see the card
--   'unlisted' — never appears in discovery; direct URL only

ALTER TABLE users
    ADD COLUMN discovery TEXT NOT NULL DEFAULT 'public';

ALTER TABLE users
    ADD CONSTRAINT users_discovery_chk
    CHECK (discovery IN ('public', 'members', 'unlisted'));
