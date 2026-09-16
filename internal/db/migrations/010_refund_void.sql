-- +goose Up
-- Admin correction for mistyped refunds: void keeps the row (who/when/why)
-- while excluding it from all totals. Sums treat NULL voided_at as active.

ALTER TABLE refunds
	ADD COLUMN voided_at TIMESTAMPTZ,
	ADD COLUMN voided_by_id UUID REFERENCES users(id) ON DELETE SET NULL,
	ADD COLUMN void_reason TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE refunds DROP COLUMN void_reason;
ALTER TABLE refunds DROP COLUMN voided_by_id;
ALTER TABLE refunds DROP COLUMN voided_at;
