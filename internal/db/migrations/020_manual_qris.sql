-- +goose Up
-- Manual QRIS: the static merchant QR is shown on the pay page and staff
-- verifies the transfer receipt in the portal. Bookings are confirmed at
-- creation (pay ahead or at the counter), so the payment no longer gates the
-- slot and holds are only kept for the dormant Midtrans mode.

ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_method_check;
ALTER TABLE payments ADD CONSTRAINT payments_method_check
    CHECK (payment_method IN ('qris', 'qris_static', 'cash'));

ALTER TABLE payments ADD COLUMN IF NOT EXISTS customer_reported_at TIMESTAMPTZ;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS verified_at TIMESTAMPTZ;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS verified_by UUID REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE payments ADD COLUMN IF NOT EXISTS review_note TEXT NOT NULL DEFAULT '';
ALTER TABLE payments ADD COLUMN IF NOT EXISTS reference TEXT NOT NULL DEFAULT '';
ALTER TABLE payments ADD COLUMN IF NOT EXISTS receipt_path TEXT NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS payment_settings (
    id SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    qris_image_path TEXT NOT NULL DEFAULT '',
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
INSERT INTO payment_settings (id) VALUES (1) ON CONFLICT (id) DO NOTHING;

-- Pay-later policy: still-valid unpaid holds become regular confirmed
-- bookings. Expired rows keep their status (the slot is already released).
UPDATE bookings
SET status = 'confirmed', hold_expires_at = NULL, updated_at = now()
WHERE status = 'pending_payment'
  AND (hold_expires_at IS NULL OR hold_expires_at > now());

-- +goose Down
UPDATE payments SET payment_method = 'qris' WHERE payment_method = 'qris_static';
ALTER TABLE payments DROP CONSTRAINT IF EXISTS payments_method_check;
ALTER TABLE payments ADD CONSTRAINT payments_method_check
    CHECK (payment_method IN ('qris', 'cash'));
ALTER TABLE payments DROP COLUMN IF EXISTS receipt_path;
ALTER TABLE payments DROP COLUMN IF EXISTS review_note;
ALTER TABLE payments DROP COLUMN IF EXISTS reference;
ALTER TABLE payments DROP COLUMN IF EXISTS verified_by;
ALTER TABLE payments DROP COLUMN IF EXISTS verified_at;
ALTER TABLE payments DROP COLUMN IF EXISTS customer_reported_at;
DROP TABLE IF EXISTS payment_settings;
