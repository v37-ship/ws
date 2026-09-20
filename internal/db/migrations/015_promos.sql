-- +goose Up
-- Promo codes for booking discounts: percent (1-100) or fixed IDR off.
-- Codes carry an active flag, minimum spend, optional validity window and
-- optional total-use cap. Bookings snapshot the applied code and discount
-- (history untouched by later code edits, same rule as ServiceName).

CREATE TABLE promo_codes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    code TEXT NOT NULL UNIQUE,
    kind TEXT NOT NULL CHECK (kind IN ('percent', 'fixed')),
    value INT NOT NULL CHECK (value > 0),
    min_spend BIGINT NOT NULL DEFAULT 0 CHECK (min_spend >= 0),
    max_uses INT CHECK (max_uses IS NULL OR max_uses > 0),
    used_count INT NOT NULL DEFAULT 0 CHECK (used_count >= 0),
    active BOOLEAN NOT NULL DEFAULT true,
    valid_from TIMESTAMPTZ,
    valid_to TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE bookings ADD COLUMN promo_code TEXT NOT NULL DEFAULT '';
ALTER TABLE bookings ADD COLUMN discount_idr BIGINT NOT NULL DEFAULT 0 CHECK (discount_idr >= 0);

-- +goose Down
ALTER TABLE bookings DROP COLUMN discount_idr;
ALTER TABLE bookings DROP COLUMN promo_code;
DROP TABLE promo_codes;
