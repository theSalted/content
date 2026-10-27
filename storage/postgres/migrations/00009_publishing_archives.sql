-- +goose Up
-- Explicit operator enrollment only; migration/startup never backfill posts.
ALTER TABLE {{schema}}.posts ADD COLUMN archive_pending boolean NOT NULL DEFAULT false;
ALTER TABLE {{schema}}.posts ADD CONSTRAINT archive_only_public_post
 CHECK(NOT archive_pending OR (kind='post' AND status='published' AND visibility='public'));
CREATE TABLE {{schema}}.publishing_archives (
 event_id uuid PRIMARY KEY,
 publication_id text NOT NULL,
 post_id text NOT NULL,
 content_id uuid NOT NULL,
 revision bigint NOT NULL CHECK(revision>0),
 destination text NOT NULL,
 source_id text NOT NULL,
 site_url text NOT NULL,
 payload bytea NOT NULL CHECK(octet_length(payload)>0 AND octet_length(payload)<=2097152),
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','leased','accepted','quarantined')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0),
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 lease_token uuid,
 lease_until timestamptz,
 last_error_code text,
 accepted_result text CHECK(accepted_result='applied'),
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(publication_id,post_id) REFERENCES {{schema}}.publishing_streams(publication_id,post_id),
 UNIQUE(publication_id,post_id),
 CHECK((state='leased' AND lease_token IS NOT NULL AND lease_until IS NOT NULL) OR (state<>'leased' AND lease_token IS NULL AND lease_until IS NULL))
);
CREATE INDEX archives_due ON {{schema}}.publishing_archives(publication_id,destination,next_attempt_at) WHERE state IN ('pending','leased');
-- No runtime grants, credentials, service or automatic network operation.
