-- scs/postgresstore session table.
--
-- scs doesn't auto-create this — required schema is documented at
-- https://github.com/alexedwards/scs/tree/master/postgresstore
--
-- Without it, the OAuth Start handler 500s on its first session.Put because
-- the LoadAndSave middleware can't commit. Don't remove this migration.

CREATE TABLE sessions (
    token  TEXT        PRIMARY KEY,
    data   BYTEA       NOT NULL,
    expiry TIMESTAMPTZ NOT NULL
);

CREATE INDEX sessions_expiry_idx ON sessions (expiry);
