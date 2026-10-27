-- +goose Up
-- Trash is a recoverable withdrawal: content/revisions/media references and
-- stable identities remain. Restore returns to draft; republishing is explicit.
ALTER TABLE {{schema}}.posts ADD COLUMN trashed_at timestamptz;
ALTER TABLE {{schema}}.posts ADD CONSTRAINT trashed_posts_are_drafts CHECK(trashed_at IS NULL OR status='draft');
CREATE INDEX posts_trash ON {{schema}}.posts(publication_id,trashed_at DESC,id) WHERE trashed_at IS NOT NULL;
-- Existing CMS API role additionally needs UPDATE(trashed_at) on posts.
-- No DELETE grant, automatic data changes, or permanent deletion endpoint.
