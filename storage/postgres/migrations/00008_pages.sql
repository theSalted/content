-- +goose Up
-- Pages share revisions/privacy controls but never join the post feed or AP.
ALTER TABLE {{schema}}.posts ADD COLUMN kind text NOT NULL DEFAULT 'post'
 CHECK(kind IN ('post','page'));
CREATE INDEX pages_title_order ON {{schema}}.posts(publication_id,title,id)
 WHERE kind='page' AND status='published' AND visibility='public';
-- Existing records remain posts; source imports select page explicitly.
