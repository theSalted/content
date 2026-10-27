-- +goose Up
-- Preserve the first publication date after an article returns to draft.
ALTER TABLE {{schema}}.posts DROP CONSTRAINT posts_check;
ALTER TABLE {{schema}}.posts ADD CONSTRAINT posts_publication_time_check
 CHECK (status <> 'published' OR published_at IS NOT NULL);

-- Existing current revisions have known metadata. Do not invent metadata for
-- any historical revision that predates this recording capability.
ALTER TABLE {{schema}}.post_revisions ADD COLUMN metadata jsonb;
UPDATE {{schema}}.post_revisions r SET metadata=jsonb_build_object(
 'id',p.id,'uuid',p.uuid,'title',p.title,'slug',p.slug,'status',p.status,
 'visibility',p.visibility,'published_at',p.published_at)
 FROM {{schema}}.posts p
 WHERE r.publication_id=p.publication_id AND r.post_id=p.id AND r.revision=p.revision;
ALTER TABLE {{schema}}.post_revisions ADD CONSTRAINT post_revision_metadata_required
 CHECK (metadata IS NOT NULL AND jsonb_typeof(metadata)='object') NOT VALID;
-- Existing unknown historical metadata may remain NULL. New inserts must carry
-- a complete snapshot. No roles, grants, external delivery or destructive Down.
