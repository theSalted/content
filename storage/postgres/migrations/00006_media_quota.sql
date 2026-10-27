-- +goose Up
-- One durable application quota covers pending and ready bytes across every
-- publication in this CMS schema. Only the migration/operator role may change it.
CREATE TABLE {{schema}}.media_quota (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 limit_bytes bigint NOT NULL CHECK(limit_bytes > 0)
);
INSERT INTO {{schema}}.media_quota(singleton,limit_bytes) VALUES(true,1000000000);
