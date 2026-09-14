-- +goose Up
-- Therapist profile photos (upload or internet link).

ALTER TABLE staff
    ADD COLUMN photo_path TEXT NOT NULL DEFAULT '',
    ADD COLUMN photo_url TEXT NOT NULL DEFAULT '';
