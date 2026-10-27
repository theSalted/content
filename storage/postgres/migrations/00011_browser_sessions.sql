-- +goose Up
-- Opaque cookies and CSRF tokens are never stored here. Scope binds sessions
-- to the publication, operator and current password digest/configuration.
CREATE TABLE {{schema}}.browser_sessions (
 scope text NOT NULL CHECK (scope ~ '^[0-9a-f]{64}$'),
 token_hash text NOT NULL CHECK (token_hash ~ '^[0-9a-f]{64}$'),
 authenticated boolean NOT NULL,
 expires_at timestamptz NOT NULL,
 PRIMARY KEY(scope,token_hash)
);
CREATE INDEX browser_session_expiry ON {{schema}}.browser_sessions(expires_at);
REVOKE ALL ON {{schema}}.browser_sessions FROM PUBLIC;
-- Runtime requires only SELECT, INSERT, DELETE on this table. No automatic
-- grants, schema ownership, credentials, or unrelated-schema access.
