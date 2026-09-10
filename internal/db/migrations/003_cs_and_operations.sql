-- +goose Up

-- 1. Users table additions: active flag, baby & mom info
ALTER TABLE users ADD COLUMN IF NOT EXISTS is_active BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE users ADD COLUMN IF NOT EXISTS baby_name TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS baby_dob DATE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS mom_condition TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS medical_notes TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS users_phone_idx ON users(phone);
CREATE INDEX IF NOT EXISTS users_is_active_idx ON users(is_active);

-- 2. Bookings table: support extended statuses, source, booking fee, attribution
ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_status_check;
ALTER TABLE bookings ADD CONSTRAINT bookings_status_check CHECK (
    status IN ('pending_payment', 'confirmed', 'checked_in', 'in_service', 'completed', 'cancelled', 'expired', 'no_show')
);

ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_staff_no_overlap;
ALTER TABLE bookings ADD CONSTRAINT bookings_staff_no_overlap
    EXCLUDE USING gist (
        staff_id WITH =,
        tstzrange(start_at, end_at, '[)') WITH &&
    ) WHERE (status IN ('pending_payment', 'confirmed', 'checked_in', 'in_service'));

ALTER TABLE bookings ADD COLUMN IF NOT EXISTS source TEXT NOT NULL DEFAULT 'web';
ALTER TABLE bookings ADD COLUMN IF NOT EXISTS booking_fee_idr BIGINT NOT NULL DEFAULT 0;
ALTER TABLE bookings ADD COLUMN IF NOT EXISTS created_by_id UUID REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE bookings ADD COLUMN IF NOT EXISTS updated_by_id UUID REFERENCES users(id) ON DELETE SET NULL;

-- 3. Payments table: payment method & purpose (cash ONLY for services, qris for booking fee and services)
ALTER TABLE payments ADD COLUMN IF NOT EXISTS payment_method TEXT NOT NULL DEFAULT 'qris';
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_method_check;
ALTER TABLE payments ADD CONSTRAINT payments_method_check CHECK (payment_method IN ('qris', 'cash'));

ALTER TABLE payments ADD COLUMN IF NOT EXISTS payment_purpose TEXT NOT NULL DEFAULT 'full';
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_purpose_check;
ALTER TABLE payments ADD CONSTRAINT payments_purpose_check CHECK (payment_purpose IN ('booking_fee', 'service', 'full'));

-- Enforce business rule: Cash can only be used to pay services, NOT booking fee
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_cash_restriction;
ALTER TABLE payments ADD CONSTRAINT payments_cash_restriction CHECK (
    NOT (payment_method = 'cash' AND payment_purpose = 'booking_fee')
);

-- 4. Staff time off / exceptions table (bidan leave, doctor appointments, emergency break)
CREATE TABLE IF NOT EXISTS staff_time_off (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    staff_id UUID NOT NULL REFERENCES staff(id) ON DELETE CASCADE,
    start_at TIMESTAMPTZ NOT NULL,
    end_at TIMESTAMPTZ NOT NULL,
    reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (end_at > start_at)
);
CREATE INDEX IF NOT EXISTS staff_time_off_staff_idx ON staff_time_off(staff_id, start_at);

-- 5. Booking logs (audit trail for status changes, reschedules, CS notes)
CREATE TABLE IF NOT EXISTS booking_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id UUID NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    notes TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS booking_logs_booking_idx ON booking_logs(booking_id, created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS booking_logs;
DROP TABLE IF EXISTS staff_time_off;

ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_cash_restriction;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_purpose_check;
ALTER TABLE payments DROP COLUMN IF EXISTS payment_purpose;
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_method_check;
ALTER TABLE payments DROP COLUMN IF EXISTS payment_method;

ALTER TABLE bookings DROP COLUMN IF EXISTS updated_by_id;
ALTER TABLE bookings DROP COLUMN IF EXISTS created_by_id;
ALTER TABLE bookings DROP COLUMN IF EXISTS booking_fee_idr;
ALTER TABLE bookings DROP COLUMN IF EXISTS source;

ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_staff_no_overlap;
ALTER TABLE bookings ADD CONSTRAINT bookings_staff_no_overlap
    EXCLUDE USING gist (
        staff_id WITH =,
        tstzrange(start_at, end_at, '[)') WITH &&
    ) WHERE (status IN ('pending_payment', 'confirmed'));

ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_status_check;
ALTER TABLE bookings ADD CONSTRAINT bookings_status_check CHECK (
    status IN ('pending_payment', 'confirmed', 'cancelled', 'expired')
);

DROP INDEX IF EXISTS users_is_active_idx;
DROP INDEX IF EXISTS users_phone_idx;
ALTER TABLE users DROP COLUMN IF EXISTS medical_notes;
ALTER TABLE users DROP COLUMN IF EXISTS mom_condition;
ALTER TABLE users DROP COLUMN IF EXISTS baby_dob;
ALTER TABLE users DROP COLUMN IF EXISTS baby_name;
ALTER TABLE users DROP COLUMN IF EXISTS is_active;
