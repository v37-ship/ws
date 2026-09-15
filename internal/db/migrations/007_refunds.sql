-- +goose Up
-- Cash-basis refunds: money returned to customers (e.g. cancelled paid
-- bookings). Monthly reports deduct refunds in the month they were recorded.

CREATE TABLE IF NOT EXISTS refunds (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id UUID NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    amount_idr BIGINT NOT NULL CHECK (amount_idr > 0),
    reason TEXT NOT NULL DEFAULT '',
    created_by_id UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS refunds_booking_idx ON refunds(booking_id);
CREATE INDEX IF NOT EXISTS refunds_created_idx ON refunds(created_at);

-- +goose Down
DROP TABLE IF EXISTS refunds;
