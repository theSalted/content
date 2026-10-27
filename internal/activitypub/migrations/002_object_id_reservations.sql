-- A URL is reserved for its entire lifetime, independent of which table holds
-- its current representation. Existing ambiguous current/retired IDs fail the
-- unique insert and roll back this migration for operator investigation.
CREATE TABLE {{schema}}.object_id_reservations (
  object_id text PRIMARY KEY,
  publication_id text NOT NULL DEFAULT current_setting('content.publication',true),
  content_id uuid,
  reserved_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE(object_id,publication_id),
  UNIQUE(object_id,publication_id,content_id)
);
-- Migration owns the tables. Disabling RLS only inside this locked transaction
-- lets the backfill cover every publication. Runtime roles cannot perform it.
ALTER TABLE {{schema}}.content_objects DISABLE ROW LEVEL SECURITY;
ALTER TABLE {{schema}}.retired_objects DISABLE ROW LEVEL SECURITY;
INSERT INTO {{schema}}.object_id_reservations(object_id,publication_id,content_id)
 SELECT object_id,publication_id,content_id FROM {{schema}}.content_objects
 UNION ALL SELECT object_id,publication_id,NULL::uuid FROM {{schema}}.retired_objects;
ALTER TABLE {{schema}}.content_objects ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{schema}}.retired_objects ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{schema}}.content_objects ADD CONSTRAINT content_object_reserved
 FOREIGN KEY(object_id,publication_id,content_id) REFERENCES {{schema}}.object_id_reservations(object_id,publication_id,content_id);
ALTER TABLE {{schema}}.retired_objects ADD CONSTRAINT retired_object_reserved
 FOREIGN KEY(object_id,publication_id) REFERENCES {{schema}}.object_id_reservations(object_id,publication_id);
ALTER TABLE {{schema}}.object_id_reservations ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{schema}}.object_id_reservations FORCE ROW LEVEL SECURITY;
CREATE POLICY publication_scope ON {{schema}}.object_id_reservations
 USING(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=object_id_reservations.publication_id AND pr.role_name=current_user))
 WITH CHECK(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=object_id_reservations.publication_id AND pr.role_name=current_user));
COMMENT ON TABLE {{schema}}.object_id_reservations IS 'Lifetime URL reservations. Runtime SELECT/INSERT only. Never delete to enable URL reuse.';
