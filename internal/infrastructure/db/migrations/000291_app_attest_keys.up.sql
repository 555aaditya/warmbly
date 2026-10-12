-- App Attest keys the native app registered: one per install, verified against
-- Apple's root, then used to check the assertion on each unauthenticated auth
-- write. Not tied to a user or a workspace.
CREATE TABLE app_attest_keys (
    key_id       TEXT PRIMARY KEY,
    public_key   BYTEA NOT NULL,
    app_id       TEXT NOT NULL,
    environment  TEXT NOT NULL CHECK (environment IN ('production', 'development')),
    counter      BIGINT NOT NULL DEFAULT 0 CHECK (counter >= 0),
    receipt      BYTEA,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ
);
