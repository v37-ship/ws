-- +goose Up
-- Service gallery: staff-managed photos and linked videos (Google Drive,
-- YouTube, Vimeo). Photos are uploads or links like the other managers;
-- videos are links only (never uploads). Items may link to a service for
-- the public filter chips; NULL means unlinked.

CREATE TABLE gallery_items (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    kind TEXT NOT NULL DEFAULT 'photo' CHECK (kind IN ('photo', 'video')),
    title TEXT NOT NULL DEFAULT '',
    caption TEXT NOT NULL DEFAULT '',
    service_id UUID REFERENCES services(id) ON DELETE SET NULL,
    image_path TEXT NOT NULL DEFAULT '',
    image_url TEXT NOT NULL DEFAULT '',
    video_url TEXT NOT NULL DEFAULT '',
    thumb_path TEXT NOT NULL DEFAULT '',
    thumb_url TEXT NOT NULL DEFAULT '',
    sort INT NOT NULL DEFAULT 0,
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX gallery_items_sort_idx ON gallery_items(sort);
CREATE INDEX gallery_items_service_idx ON gallery_items(service_id);

-- +goose Down
DROP INDEX gallery_items_service_idx;
DROP INDEX gallery_items_sort_idx;
DROP TABLE gallery_items;
