-- +goose Up
-- Per-weekday service availability windows + service photos.

CREATE TABLE service_hours (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    service_id UUID NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    weekday INTEGER NOT NULL CHECK (weekday BETWEEN 0 AND 6),
    start_time TIME NOT NULL,
    end_time TIME NOT NULL,
    CHECK (end_time > start_time),
    UNIQUE (service_id, weekday)
);
CREATE INDEX idx_service_hours_service ON service_hours(service_id);

ALTER TABLE services
    ADD COLUMN image_path TEXT NOT NULL DEFAULT '',
    ADD COLUMN image_url TEXT NOT NULL DEFAULT '';
