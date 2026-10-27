-- +goose Up
-- Operator-only archives. Runtime/API roles receive no access to these tables.
CREATE TABLE {{schema}}.source_import_batches (
 publication_id text NOT NULL,
 source_namespace text NOT NULL,
 export_sha256 text NOT NULL CHECK (export_sha256 ~ '^[0-9a-f]{64}$'),
 raw_export text NOT NULL,
 public_only boolean NOT NULL,
 source_url text NOT NULL DEFAULT '',
 public_snapshot_sha256 text NOT NULL DEFAULT '',
 raw_public_snapshot text NOT NULL DEFAULT '',
 imported_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(publication_id,source_namespace,export_sha256)
);
CREATE TABLE {{schema}}.source_import_records (
 publication_id text NOT NULL,
 source_namespace text NOT NULL,
 object_kind text NOT NULL CHECK(object_kind IN ('posts','pages')),
 source_id text NOT NULL,
 source_sha256 text NOT NULL CHECK (source_sha256 ~ '^[0-9a-f]{64}$'),
 source_record jsonb NOT NULL,
 state text NOT NULL CHECK(state IN ('imported','quarantined')),
 reason text NOT NULL,
 cms_id text,
 cms_uuid uuid,
 imported_revision bigint,
 PRIMARY KEY(publication_id,source_namespace,object_kind,source_id),
 UNIQUE(publication_id,cms_id),
 CHECK((state='imported' AND cms_id IS NOT NULL AND cms_uuid IS NOT NULL AND imported_revision=1 AND reason='') OR (state='quarantined' AND cms_id IS NULL AND reason<>''))
);
