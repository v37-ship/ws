-- +goose Up
-- Customer testimonials with admin moderation: new entries stay 'pending'
-- until approved; only 'approved' rows render on the homepage carousel.
-- Photos follow the service/staff pattern: local upload path or URL link.

CREATE TABLE testimonials (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id UUID REFERENCES users(id) ON DELETE SET NULL,
    name TEXT NOT NULL,
    age INT NOT NULL DEFAULT 0,
    photo_path TEXT NOT NULL DEFAULT '',
    photo_url TEXT NOT NULL DEFAULT '',
    rating SMALLINT NOT NULL CHECK (rating BETWEEN 1 AND 5),
    message TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'hidden')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX testimonials_status_idx ON testimonials(status);

ALTER TABLE users ADD COLUMN photo_url TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN photo_path TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE users DROP COLUMN photo_path;
ALTER TABLE users DROP COLUMN photo_url;
DROP INDEX testimonials_status_idx;
DROP TABLE testimonials;
