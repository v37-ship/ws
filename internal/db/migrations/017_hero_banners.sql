-- +goose Up
-- Homepage hero banners: staff-managed slideshow photos. Zero rows keep
-- the built-in default image; one active banner renders statically.

CREATE TABLE hero_banners (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    image_path TEXT NOT NULL DEFAULT '',
    image_url TEXT NOT NULL DEFAULT '',
    sort INT NOT NULL DEFAULT 0,
    active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX hero_banners_sort_idx ON hero_banners(sort);

-- +goose Down
DROP INDEX hero_banners_sort_idx;
DROP TABLE hero_banners;
