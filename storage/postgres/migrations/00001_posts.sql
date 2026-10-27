-- +goose Up
CREATE TABLE {{schema}}.installation (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
 installation_id text NOT NULL
);
CREATE TABLE {{schema}}.posts (
 publication_id text NOT NULL,
 id text NOT NULL CHECK (id ~ '^[0-9a-f]{24}$'),
 uuid uuid NOT NULL,
 title text NOT NULL CHECK (length(title) BETWEEN 1 AND 255),
 slug text NOT NULL CHECK (length(slug) BETWEEN 1 AND 191),
 status text NOT NULL CHECK (status IN ('draft','published')),
 visibility text NOT NULL CHECK (visibility = 'public'),
 revision bigint NOT NULL CHECK (revision > 0),
 created_at timestamptz NOT NULL,
 updated_at timestamptz NOT NULL,
 published_at timestamptz,
 PRIMARY KEY (publication_id,id),
 UNIQUE (publication_id,slug),
 UNIQUE (publication_id,uuid),
 CHECK ((status='published' AND published_at IS NOT NULL) OR (status='draft' AND published_at IS NULL))
);
CREATE TABLE {{schema}}.post_revisions (
 publication_id text NOT NULL,
 post_id text NOT NULL,
 revision bigint NOT NULL,
 source_html text NOT NULL,
 html text NOT NULL,
 renderer_version text NOT NULL,
 created_at timestamptz NOT NULL,
 PRIMARY KEY (publication_id,post_id,revision),
 FOREIGN KEY (publication_id,post_id) REFERENCES {{schema}}.posts(publication_id,id)
);
CREATE TABLE {{schema}}.outbox_events (
 event_id uuid PRIMARY KEY,
 publication_id text NOT NULL,
 post_id text NOT NULL,
 revision bigint NOT NULL,
 event_type text NOT NULL,
 schema_version integer NOT NULL CHECK (schema_version=1),
 snapshot jsonb NOT NULL,
 created_at timestamptz NOT NULL,
 FOREIGN KEY (publication_id,post_id,revision) REFERENCES {{schema}}.post_revisions(publication_id,post_id,revision),
 UNIQUE (publication_id,post_id,revision,event_type)
);
CREATE INDEX posts_public_order ON {{schema}}.posts(publication_id,published_at DESC,id DESC) WHERE status='published' AND visibility='public';
-- Forward-only: this command does not expose Down. Retain data and use a reviewed forward fix.
