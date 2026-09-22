-- +goose Up
-- Guest checkout: bookings may belong to no account. Guest identity lives
-- on the row (name/phone required, email optional) and guests manage the
-- booking through an unguessable access token (revocable by NULLing it).

ALTER TABLE bookings ALTER COLUMN customer_id DROP NOT NULL;
ALTER TABLE bookings ADD COLUMN guest_name TEXT NOT NULL DEFAULT '';
ALTER TABLE bookings ADD COLUMN guest_phone TEXT NOT NULL DEFAULT '';
ALTER TABLE bookings ADD COLUMN guest_email TEXT NOT NULL DEFAULT '';
ALTER TABLE bookings ADD COLUMN access_token TEXT UNIQUE;

-- +goose Down
DELETE FROM bookings WHERE customer_id IS NULL;
ALTER TABLE bookings DROP COLUMN access_token;
ALTER TABLE bookings DROP COLUMN guest_email;
ALTER TABLE bookings DROP COLUMN guest_phone;
ALTER TABLE bookings DROP COLUMN guest_name;
ALTER TABLE bookings ALTER COLUMN customer_id SET NOT NULL;
