-- +goose Up
-- Waiting-room TV kiosk tokens. One row per screen: the token is the only
-- credential the TV presents, it is bound to the branch whose queue it may
-- show (ruling: location-bound, so branch #2 can never show branch #1's
-- schedule), and revoking sets revoked_at, which kills it on the next poll.
-- Additive only: no existing table changes.

CREATE TABLE IF NOT EXISTS tv_tokens (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    token TEXT NOT NULL UNIQUE,
    label TEXT NOT NULL DEFAULT '',
    location_id UUID NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ
);

-- +goose Down
DROP TABLE IF EXISTS tv_tokens;
