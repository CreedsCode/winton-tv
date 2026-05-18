-- Stream metadata set from the dashboard, shown on the channel page.
-- Plain text for V1; future migration can add tags / rich format.

ALTER TABLE users
    ADD COLUMN title        TEXT NOT NULL DEFAULT '',
    ADD COLUMN description  TEXT NOT NULL DEFAULT '';
