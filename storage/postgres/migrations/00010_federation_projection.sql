-- +goose Up
-- A generation is the revision starting the current public lifecycle. It is
-- maintained even for older API binaries, never inferred from delivery cursors.
ALTER TABLE {{schema}}.posts ADD COLUMN public_generation bigint NOT NULL DEFAULT 0;
UPDATE {{schema}}.posts SET public_generation=revision WHERE kind='post' AND status='published' AND visibility='public';
ALTER TABLE {{schema}}.posts ADD CONSTRAINT valid_public_generation CHECK(public_generation>=0 AND public_generation<=revision);
-- +goose StatementBegin
CREATE FUNCTION {{schema}}.maintain_public_generation() RETURNS trigger
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF NEW.kind='post' AND NEW.status='published' AND NEW.visibility='public' THEN
  IF TG_OP='UPDATE' THEN
   IF OLD.kind='post' AND OLD.status='published' AND OLD.visibility='public' THEN
    NEW.public_generation:=OLD.public_generation;
   ELSE
    IF NEW.revision<=OLD.revision THEN RAISE EXCEPTION 'publication requires a new revision'; END IF;
    NEW.public_generation:=NEW.revision;
   END IF;
  ELSE
   NEW.public_generation:=NEW.revision;
  END IF;
 ELSE
  NEW.public_generation:=0;
 END IF;
 RETURN NEW;
END;
$$;
-- +goose StatementEnd
REVOKE ALL ON FUNCTION {{schema}}.maintain_public_generation() FROM PUBLIC;
CREATE TRIGGER maintain_public_generation BEFORE INSERT OR UPDATE ON {{schema}}.posts FOR EACH ROW EXECUTE FUNCTION {{schema}}.maintain_public_generation();
CREATE TABLE {{schema}}.federation_projections (
 publication_id text NOT NULL,
 post_id text NOT NULL,
 destination text NOT NULL,
 public_generation bigint NOT NULL CHECK(public_generation>0),
 object_instance uuid NOT NULL,
 object_url text NOT NULL CHECK(octet_length(object_url) BETWEEN 1 AND 2048),
 acknowledged_revision bigint NOT NULL CHECK(acknowledged_revision>=public_generation),
 event_id uuid NOT NULL,
 acknowledged_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(publication_id,post_id,destination),
 FOREIGN KEY(publication_id,post_id) REFERENCES {{schema}}.posts(publication_id,id)
);
-- No automatic archive enrollment, replay, role grants or network calls.
ALTER TABLE {{schema}}.federation_projections ADD COLUMN confirmed boolean NOT NULL DEFAULT true;
CREATE TABLE {{schema}}.federation_resolution_jobs (
 publication_id text NOT NULL,
 event_id uuid NOT NULL,
 destination text NOT NULL,
 post_id text NOT NULL,
 payload bytea NOT NULL CHECK(octet_length(payload)>0 AND octet_length(payload)<=2097152),
 lane text NOT NULL CHECK(lane IN ('live','archive')),
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','leased','complete','quarantined')),
 attempts integer NOT NULL DEFAULT 0 CHECK(attempts>=0),
 next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 lease_token uuid,
 lease_until timestamptz,
 last_error_code text,
 PRIMARY KEY(publication_id,event_id,destination),
 FOREIGN KEY(publication_id,post_id) REFERENCES {{schema}}.posts(publication_id,id),
 CHECK((state='leased' AND lease_token IS NOT NULL AND lease_until IS NOT NULL) OR (state<>'leased' AND lease_token IS NULL AND lease_until IS NULL))
);
CREATE INDEX federation_resolution_due ON {{schema}}.federation_resolution_jobs(publication_id,destination,next_attempt_at) WHERE state IN ('pending','leased');
