-- +goose Up
-- Bilingual service catalog: Indonesian name/description shown when the
-- visitor uses ?lang=id (or the ID cookie); empty falls back to name/description.
-- Backfill-safe: existing rows default to '', meaning "same as canonical".

ALTER TABLE services
	ADD COLUMN name_id TEXT NOT NULL DEFAULT '',
	ADD COLUMN description_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE services DROP COLUMN description_id;
ALTER TABLE services DROP COLUMN name_id;
