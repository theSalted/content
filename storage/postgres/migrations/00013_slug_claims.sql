-- +goose Up
CREATE TABLE {{schema}}.post_slug_claims (
 publication_id text NOT NULL,
 slug text NOT NULL CHECK(length(slug) BETWEEN 1 AND 191 AND slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
 post_id text NOT NULL,
 ever_public boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(publication_id,slug),
 FOREIGN KEY(publication_id,post_id) REFERENCES {{schema}}.posts(publication_id,id)
);
INSERT INTO {{schema}}.post_slug_claims(publication_id,slug,post_id,ever_public)
 SELECT p.publication_id,p.slug,p.id,
 (p.status='published' AND p.visibility='public') OR EXISTS(
 SELECT 1 FROM {{schema}}.post_revisions r WHERE r.publication_id=p.publication_id AND r.post_id=p.id
 AND r.metadata->>'slug'=p.slug AND r.metadata->>'status'='published' AND r.metadata->>'visibility'='public')
 FROM {{schema}}.posts p;
-- Invoker security: runtime requires only the explicit new table/column grants.
-- +goose StatementBegin
CREATE FUNCTION {{schema}}.maintain_slug_claim() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE claimed text;
BEGIN
 INSERT INTO {{schema}}.post_slug_claims AS c(publication_id,slug,post_id,ever_public)
 VALUES(NEW.publication_id,NEW.slug,NEW.id,NEW.status='published' AND NEW.visibility='public')
 ON CONFLICT(publication_id,slug) DO UPDATE SET ever_public=c.ever_public OR EXCLUDED.ever_public
 WHERE c.post_id=EXCLUDED.post_id RETURNING post_id INTO claimed;
 IF claimed IS NULL THEN RAISE EXCEPTION 'slug is reserved' USING ERRCODE='23505'; END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON {{schema}}.post_slug_claims FROM PUBLIC;
REVOKE ALL ON FUNCTION {{schema}}.maintain_slug_claim() FROM PUBLIC;
CREATE TRIGGER maintain_slug_claim AFTER INSERT OR UPDATE OF slug,status,visibility ON {{schema}}.posts
 FOR EACH ROW EXECUTE FUNCTION {{schema}}.maintain_slug_claim();
