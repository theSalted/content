-- +goose Up
ALTER TABLE {{schema}}.posts DROP CONSTRAINT posts_status_check;
ALTER TABLE {{schema}}.posts ADD CONSTRAINT posts_status_check CHECK(status IN ('draft','published','scheduled'));
CREATE TABLE {{schema}}.publication_schedules (
 publication_id text NOT NULL,
 post_id text NOT NULL,
 schedule_id uuid NOT NULL,
 due_at timestamptz NOT NULL,
 expected_revision bigint NOT NULL CHECK(expected_revision>0),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0),
 next_attempt_at timestamptz NOT NULL,
 last_error_code text CHECK(last_error_code IN ('publication_failed','revision_mismatch')),
 PRIMARY KEY(publication_id,post_id),
 FOREIGN KEY(publication_id,post_id,expected_revision) REFERENCES {{schema}}.post_revisions(publication_id,post_id,revision)
);
CREATE INDEX publication_schedule_due ON {{schema}}.publication_schedules(publication_id,next_attempt_at,post_id);
REVOKE ALL ON {{schema}}.publication_schedules FROM PUBLIC;
-- Empty on migration: never infer jobs from dates, imports or existing drafts.
