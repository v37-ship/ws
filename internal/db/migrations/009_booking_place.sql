-- +goose Up
-- Homecare fulfillment mode: bookings happen either onsite at the branch
-- or as a homecare visit at the customer's address.
-- Backfill-safe: existing rows default to 'onsite', address empty.

ALTER TABLE bookings
	ADD COLUMN place TEXT NOT NULL DEFAULT 'onsite'
		CHECK (place IN ('onsite', 'homecare')),
	ADD COLUMN visit_address TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE bookings DROP COLUMN visit_address;
ALTER TABLE bookings DROP COLUMN place;
