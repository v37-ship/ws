-- +goose Up
-- Waiting-room TV device pairing (plan: tv-pairing). Two additive tables:
--
--   tv_devices  — the durable credential. One row per paired screen; the
--                 secret is stored HASHED (sha256 hex, ruling #9 — a 30-day
--                 credential does not get the plaintext tv_tokens treatment),
--                 because the device cookie never expires in a session sense.
--   tv_pairings — the ephemeral handshake. The TV shows `code` for five
--                 minutes and keeps `pending_key` in an HttpOnly cookie; the CS
--                 claims the code at the portal, which binds location_id, mints
--                 the device and sets device_id (ruling #6), and the TV's poll
--                 then reads the device and receives its cookie.
--
-- The device secret's plaintext is the pairing's pending_key: the portal
-- hashes that same 32-byte hex value into tv_devices.secret_hash, and the TV
-- already holds it, so no secret ever travels to the counter. Rows are swept
-- once a minute (expired at once, consumed after a ten-minute grace so the
-- TV's last poll can still read its pairing), so this unauthenticated-write
-- surface stays bounded.
--
-- Additive only: 023's tv_tokens is untouched and keeps working.

CREATE TABLE IF NOT EXISTS tv_devices (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    secret_hash TEXT NOT NULL,
    label TEXT NOT NULL DEFAULT '',
    location_id UUID NOT NULL REFERENCES locations(id) ON DELETE CASCADE,
    paired_by UUID REFERENCES users(id) ON DELETE SET NULL,
    paired_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS tv_pairings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    pending_key TEXT NOT NULL UNIQUE,
    code TEXT NOT NULL,
    location_id UUID REFERENCES locations(id) ON DELETE CASCADE,
    device_id UUID REFERENCES tv_devices(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Consume looks a live code up by value; the table is swept so the index stays
-- tiny. No uniqueness: two screens may theoretically draw the same six digits,
-- and the oldest live row wins (the branch comes from the CS either way).
CREATE INDEX IF NOT EXISTS tv_pairings_code_idx ON tv_pairings (code);

-- +goose Down
DROP TABLE IF EXISTS tv_pairings;
DROP TABLE IF EXISTS tv_devices;
