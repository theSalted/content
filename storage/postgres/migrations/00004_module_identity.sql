-- +goose Up
-- Preflight has already established an empty namespace or recognized legacy CMS
-- ownership. A matching installation ID alone is never proof of module identity.
ALTER TABLE {{schema}}.installation ADD COLUMN module_id text NOT NULL DEFAULT 'cms' CHECK(module_id='cms');
