CREATE TABLE {{schema}}.publication_roles(publication_id text NOT NULL,role_name name NOT NULL,PRIMARY KEY(publication_id,role_name));
CREATE TABLE {{schema}}.identities (
  publication_id text NOT NULL DEFAULT current_setting('content.publication',true),
  actor_id text NOT NULL,
  PRIMARY KEY(publication_id,actor_id),
  key_id text NOT NULL UNIQUE,
  public_jwk jsonb NOT NULL,
  private_jwk jsonb NOT NULL,
  imported_at timestamptz NOT NULL DEFAULT now()
);
COMMENT ON TABLE {{schema}}.identities IS 'Existing identity only. No runtime insert, regeneration, or rotation.';

CREATE TABLE {{schema}}.content_objects (
  publication_id text NOT NULL DEFAULT current_setting('content.publication',true),
  content_id uuid NOT NULL,
  PRIMARY KEY(publication_id,content_id),
  object_id text NOT NULL UNIQUE,
  instance_id uuid NOT NULL,
  source_id text NOT NULL,
  source_epoch bigint NOT NULL CHECK (source_epoch > 0),
  revision bigint NOT NULL DEFAULT 0 CHECK (revision >= 0),
  state text NOT NULL CHECK (state IN ('unpublished','public','withdrawn','deleted')),
  payload jsonb,
  published_at timestamptz,
  changed_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((state = 'public') = (payload IS NOT NULL))
);
CREATE TABLE {{schema}}.publishing_events (
  publication_id text NOT NULL DEFAULT current_setting('content.publication',true),
  event_id uuid NOT NULL,
  PRIMARY KEY(publication_id,event_id),
  content_id uuid NOT NULL,
  FOREIGN KEY(publication_id,content_id) REFERENCES {{schema}}.content_objects(publication_id,content_id),
  payload_hash text NOT NULL,
  source_id text NOT NULL,
  source_epoch bigint NOT NULL,
  revision bigint NOT NULL,
  result text NOT NULL CHECK (result IN ('applied','stale')),
  received_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE {{schema}}.activities (
  publication_id text NOT NULL DEFAULT current_setting('content.publication',true),
  sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
  id text NOT NULL,
  PRIMARY KEY(publication_id,id),
  content_id uuid,
  FOREIGN KEY(publication_id,content_id) REFERENCES {{schema}}.content_objects(publication_id,content_id),
  kind text NOT NULL,
  payload jsonb NOT NULL,
  public boolean NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX public_activities ON {{schema}}.activities(created_at DESC, id DESC) WHERE public;
CREATE TABLE {{schema}}.inbox_receipts (
  publication_id text NOT NULL DEFAULT current_setting('content.publication',true),
  id text NOT NULL,
  PRIMARY KEY(publication_id,id),
  actor_id text NOT NULL,
  kind text NOT NULL,
  received_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE {{schema}}.followers (
  publication_id text NOT NULL DEFAULT current_setting('content.publication',true),
  actor_id text NOT NULL,
  PRIMARY KEY(publication_id,actor_id),
  inbox text NOT NULL,
  shared_inbox text,
  follow_id text,
  accepted_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE {{schema}}.undone_follows (
  publication_id text NOT NULL DEFAULT current_setting('content.publication',true),
  follow_id text NOT NULL,
  PRIMARY KEY(publication_id,follow_id),
  actor_id text NOT NULL,
  received_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE {{schema}}.outbound_intents (
  publication_id text NOT NULL DEFAULT current_setting('content.publication',true),
  sequence bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
  id uuid PRIMARY KEY,
  activity_id text NOT NULL,
  FOREIGN KEY(publication_id,activity_id) REFERENCES {{schema}}.activities(publication_id,id),
  recipient_id text NOT NULL,
  inbox text NOT NULL,
  shared_inbox text,
  state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','leased','enqueued','dead')),
  attempts integer NOT NULL DEFAULT 0,
  available_at timestamptz NOT NULL DEFAULT now(),
  lease_until timestamptz,
  lease_token uuid,
  last_error text,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (publication_id,activity_id,recipient_id)
);
CREATE INDEX pending_intents ON {{schema}}.outbound_intents(available_at) WHERE state IN ('pending','leased');
CREATE TABLE {{schema}}.delivery_failures (
  publication_id text NOT NULL DEFAULT current_setting('content.publication',true),
  id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  activity_id text,
  error_code text NOT NULL,
  occurred_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE {{schema}}.worker_heartbeats (
  publication_id text NOT NULL DEFAULT current_setting('content.publication',true),
  name text NOT NULL,
  PRIMARY KEY(publication_id,name),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE {{schema}}.transport_jobs (
  publication_id text NOT NULL DEFAULT current_setting('content.publication',true),
  sequence bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  message jsonb NOT NULL,
  ordering_key text,
  attempts integer NOT NULL DEFAULT 0,
  available_at timestamptz NOT NULL DEFAULT now(),
  dead boolean NOT NULL DEFAULT false,
  last_error text
);
CREATE INDEX ready_transport ON {{schema}}.transport_jobs(available_at,sequence) WHERE NOT dead;
CREATE INDEX ordered_transport ON {{schema}}.transport_jobs(ordering_key,sequence) WHERE NOT dead;

CREATE TABLE {{schema}}.federation_kv (publication_id text NOT NULL DEFAULT current_setting('content.publication',true), key text[] NOT NULL,value jsonb NOT NULL,expires_at timestamptz,PRIMARY KEY(publication_id,key));
CREATE UNIQUE INDEX identity_actor_unique ON {{schema}}.identities(actor_id);
CREATE UNIQUE INDEX identity_publication_unique ON {{schema}}.identities(publication_id);
ALTER TABLE {{schema}}.identities ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{schema}}.identities FORCE ROW LEVEL SECURITY;
CREATE POLICY publication_scope ON {{schema}}.identities USING(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=identities.publication_id AND pr.role_name=current_user)) WITH CHECK(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=identities.publication_id AND pr.role_name=current_user));
ALTER TABLE {{schema}}.content_objects ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{schema}}.content_objects FORCE ROW LEVEL SECURITY;
CREATE POLICY publication_scope ON {{schema}}.content_objects USING(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=content_objects.publication_id AND pr.role_name=current_user)) WITH CHECK(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=content_objects.publication_id AND pr.role_name=current_user));
ALTER TABLE {{schema}}.publishing_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{schema}}.publishing_events FORCE ROW LEVEL SECURITY;
CREATE POLICY publication_scope ON {{schema}}.publishing_events USING(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=publishing_events.publication_id AND pr.role_name=current_user)) WITH CHECK(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=publishing_events.publication_id AND pr.role_name=current_user));
ALTER TABLE {{schema}}.activities ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{schema}}.activities FORCE ROW LEVEL SECURITY;
CREATE POLICY publication_scope ON {{schema}}.activities USING(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=activities.publication_id AND pr.role_name=current_user)) WITH CHECK(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=activities.publication_id AND pr.role_name=current_user));
ALTER TABLE {{schema}}.inbox_receipts ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{schema}}.inbox_receipts FORCE ROW LEVEL SECURITY;
CREATE POLICY publication_scope ON {{schema}}.inbox_receipts USING(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=inbox_receipts.publication_id AND pr.role_name=current_user)) WITH CHECK(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=inbox_receipts.publication_id AND pr.role_name=current_user));
ALTER TABLE {{schema}}.followers ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{schema}}.followers FORCE ROW LEVEL SECURITY;
CREATE POLICY publication_scope ON {{schema}}.followers USING(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=followers.publication_id AND pr.role_name=current_user)) WITH CHECK(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=followers.publication_id AND pr.role_name=current_user));
ALTER TABLE {{schema}}.undone_follows ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{schema}}.undone_follows FORCE ROW LEVEL SECURITY;
CREATE POLICY publication_scope ON {{schema}}.undone_follows USING(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=undone_follows.publication_id AND pr.role_name=current_user)) WITH CHECK(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=undone_follows.publication_id AND pr.role_name=current_user));
ALTER TABLE {{schema}}.outbound_intents ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{schema}}.outbound_intents FORCE ROW LEVEL SECURITY;
CREATE POLICY publication_scope ON {{schema}}.outbound_intents USING(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=outbound_intents.publication_id AND pr.role_name=current_user)) WITH CHECK(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=outbound_intents.publication_id AND pr.role_name=current_user));
ALTER TABLE {{schema}}.delivery_failures ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{schema}}.delivery_failures FORCE ROW LEVEL SECURITY;
CREATE POLICY publication_scope ON {{schema}}.delivery_failures USING(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=delivery_failures.publication_id AND pr.role_name=current_user)) WITH CHECK(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=delivery_failures.publication_id AND pr.role_name=current_user));
ALTER TABLE {{schema}}.worker_heartbeats ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{schema}}.worker_heartbeats FORCE ROW LEVEL SECURITY;
CREATE POLICY publication_scope ON {{schema}}.worker_heartbeats USING(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=worker_heartbeats.publication_id AND pr.role_name=current_user)) WITH CHECK(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=worker_heartbeats.publication_id AND pr.role_name=current_user));
ALTER TABLE {{schema}}.transport_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{schema}}.transport_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY publication_scope ON {{schema}}.transport_jobs USING(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=transport_jobs.publication_id AND pr.role_name=current_user)) WITH CHECK(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=transport_jobs.publication_id AND pr.role_name=current_user));
ALTER TABLE {{schema}}.federation_kv ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{schema}}.federation_kv FORCE ROW LEVEL SECURITY;
CREATE POLICY publication_scope ON {{schema}}.federation_kv USING(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=federation_kv.publication_id AND pr.role_name=current_user)) WITH CHECK(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=federation_kv.publication_id AND pr.role_name=current_user));

CREATE TABLE {{schema}}.retired_objects(publication_id text NOT NULL DEFAULT current_setting('content.publication',true),object_id text NOT NULL UNIQUE,published_at timestamptz,changed_at timestamptz NOT NULL,PRIMARY KEY(publication_id,object_id));
ALTER TABLE {{schema}}.retired_objects ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{schema}}.retired_objects FORCE ROW LEVEL SECURITY;
CREATE POLICY publication_scope ON {{schema}}.retired_objects USING(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=retired_objects.publication_id AND pr.role_name=current_user)) WITH CHECK(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=retired_objects.publication_id AND pr.role_name=current_user));
