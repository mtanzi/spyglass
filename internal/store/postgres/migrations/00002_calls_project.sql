-- The project of a call (ADR 0059): a derived name, never a path. It is null
-- when no source gave one. Adding a nullable column rewrites no rows.

-- +goose Up
ALTER TABLE calls ADD COLUMN project text;

-- +goose Down
ALTER TABLE calls DROP COLUMN project;
