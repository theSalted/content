-- +goose Up
CREATE TABLE {{schema}}.media_objects (
 publication_id text NOT NULL,
 id text NOT NULL CHECK (id ~ '^[0-9a-f]{24}$'),
 object_key text NOT NULL,
 sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
 mime text NOT NULL CHECK (mime IN ('image/png','image/jpeg')),
 extension text NOT NULL CHECK (extension IN ('png','jpg')),
 byte_size bigint NOT NULL CHECK (byte_size BETWEEN 1 AND 10485760),
 width integer NOT NULL CHECK (width BETWEEN 1 AND 12000),
 height integer NOT NULL CHECK (height BETWEEN 1 AND 12000),
 state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending','ready')),
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 PRIMARY KEY(publication_id,id),
 UNIQUE(publication_id,sha256),
 UNIQUE(object_key),
 CHECK(width::bigint*height::bigint<=24000000)
);
CREATE TABLE {{schema}}.post_media (
 publication_id text NOT NULL,
 post_id text NOT NULL,
 media_id text NOT NULL,
 PRIMARY KEY(publication_id,post_id,media_id),
 FOREIGN KEY(publication_id,post_id) REFERENCES {{schema}}.posts(publication_id,id),
 FOREIGN KEY(publication_id,media_id) REFERENCES {{schema}}.media_objects(publication_id,id)
);
CREATE INDEX post_media_asset ON {{schema}}.post_media(publication_id,media_id);
CREATE INDEX media_pending_cleanup ON {{schema}}.media_objects(publication_id,created_at) WHERE state='pending';
