-- +goose Up
-- No automatic backfill: existing posts/outbox rows remain un-enrolled.
CREATE TABLE {{schema}}.publishing_streams (
 publication_id text NOT NULL,
 post_id text NOT NULL,
 content_id uuid NOT NULL,
 destination text NOT NULL,
 source_id text NOT NULL,
 source_epoch bigint NOT NULL DEFAULT 1 CHECK(source_epoch=1),
 site_url text NOT NULL,
 last_revision bigint NOT NULL DEFAULT 0 CHECK(last_revision>=0),
 object_instance uuid,
 lifecycle text NOT NULL DEFAULT 'unpublished' CHECK(lifecycle IN ('unpublished','public','withdrawn')),
 blocked_reason text,
 PRIMARY KEY(publication_id,post_id),
 FOREIGN KEY(publication_id,post_id) REFERENCES {{schema}}.posts(publication_id,id),
 CHECK ((lifecycle='unpublished' AND object_instance IS NULL) OR (lifecycle<>'unpublished' AND object_instance IS NOT NULL))
);
CREATE TABLE {{schema}}.publishing_deliveries (
 event_id uuid PRIMARY KEY REFERENCES {{schema}}.outbox_events(event_id),
 publication_id text NOT NULL,
 post_id text NOT NULL,
 revision bigint NOT NULL CHECK(revision>0),
 destination text NOT NULL,
 payload bytea NOT NULL CHECK(octet_length(payload)>0 AND octet_length(payload)<=2097152),
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','leased','accepted','quarantined')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0),
 next_attempt_at timestamptz NOT NULL DEFAULT now(),
 lease_token uuid,
 lease_until timestamptz,
 last_error_code text,
 accepted_result text CHECK(accepted_result IN ('applied','stale')),
 created_at timestamptz NOT NULL DEFAULT now(),
 FOREIGN KEY(publication_id,post_id) REFERENCES {{schema}}.publishing_streams(publication_id,post_id),
 UNIQUE(publication_id,post_id,revision),
 CHECK((state='leased' AND lease_token IS NOT NULL AND lease_until IS NOT NULL) OR (state<>'leased' AND lease_token IS NULL AND lease_until IS NULL))
);
CREATE INDEX publishing_due ON {{schema}}.publishing_deliveries(publication_id,destination,next_attempt_at) WHERE state IN ('pending','leased');
-- Runtime grants remain an explicit operator concern; no roles or credentials created.
