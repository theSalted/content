CREATE TABLE {{schema}}.feedback (
 publication_id text NOT NULL DEFAULT current_setting('content.publication',true),
 id text NOT NULL, activity_id text NOT NULL, actor_id text NOT NULL,
 target text NOT NULL, kind text NOT NULL CHECK(kind IN ('reply','like','boost')),
 html text NOT NULL DEFAULT '', source_updated_at timestamptz NOT NULL,
 state text NOT NULL DEFAULT 'pending' CHECK(state IN ('pending','approved','rejected','deleted')),
 received_at timestamptz NOT NULL DEFAULT now(), moderated_at timestamptz,
 PRIMARY KEY(publication_id,id)
);
CREATE INDEX feedback_moderation ON {{schema}}.feedback(publication_id,state,received_at);
CREATE TABLE {{schema}}.feedback_tombstones (
 publication_id text NOT NULL DEFAULT current_setting('content.publication',true),
 id text NOT NULL, actor_id text NOT NULL, received_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(publication_id,id,actor_id)
);
CREATE TABLE {{schema}}.feedback_blocks (
 publication_id text NOT NULL DEFAULT current_setting('content.publication',true),
 actor_id text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(publication_id,actor_id)
);
ALTER TABLE {{schema}}.feedback ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{schema}}.feedback FORCE ROW LEVEL SECURITY;
CREATE POLICY publication_scope ON {{schema}}.feedback USING(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=feedback.publication_id AND pr.role_name=current_user)) WITH CHECK(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=feedback.publication_id AND pr.role_name=current_user));
ALTER TABLE {{schema}}.feedback_tombstones ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{schema}}.feedback_tombstones FORCE ROW LEVEL SECURITY;
CREATE POLICY publication_scope ON {{schema}}.feedback_tombstones USING(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=feedback_tombstones.publication_id AND pr.role_name=current_user)) WITH CHECK(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=feedback_tombstones.publication_id AND pr.role_name=current_user));
ALTER TABLE {{schema}}.feedback_blocks ENABLE ROW LEVEL SECURITY;
ALTER TABLE {{schema}}.feedback_blocks FORCE ROW LEVEL SECURITY;
CREATE POLICY publication_scope ON {{schema}}.feedback_blocks USING(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=feedback_blocks.publication_id AND pr.role_name=current_user)) WITH CHECK(publication_id=current_setting('content.publication',true) AND EXISTS(SELECT 1 FROM {{schema}}.publication_roles pr WHERE pr.publication_id=feedback_blocks.publication_id AND pr.role_name=current_user));
