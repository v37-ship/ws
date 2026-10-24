-- +goose Up
-- Homecare transport fee: distance bands measured from the branch to the
-- customer's address, priced round-trip (PP). Bookings snapshot the band's
-- fee so later table edits never move old money (same rule as promo codes
-- and service names).

CREATE TABLE distance_zones (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    min_km NUMERIC(4,1) NOT NULL CHECK (min_km >= 0),
    max_km NUMERIC(4,1) NOT NULL CHECK (max_km >= min_km),
    fee_idr BIGINT NOT NULL CHECK (fee_idr >= 0),
    active BOOLEAN NOT NULL DEFAULT true,
    sort INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE bookings
    ADD COLUMN distance_km NUMERIC(4,1),
    ADD COLUMN distance_fee_idr BIGINT NOT NULL DEFAULT 0 CHECK (distance_fee_idr >= 0);

-- +goose Down
ALTER TABLE bookings DROP COLUMN distance_fee_idr;
ALTER TABLE bookings DROP COLUMN distance_km;
DROP TABLE distance_zones;
