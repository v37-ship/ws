-- +goose Up
-- Payroll (plan: payroll). Two additive tables:
--
--   staff_comp    — the pay-rate timeline. Append-only: a raise or a change of
--                   commission base is a new row with a later effective_from,
--                   never an edit, so any past period still resolves to the
--                   rate that was in force then (a future effective_from just
--                   queues a raise). Resolution is "newest row with
--                   effective_from <= the period's start".
--   staff_payouts — the immutable "this period is paid" snapshot. The numbers
--                   are copied in at mark-paid time; a correction is a new row
--                   with a note, never an edit. The unique key makes a period
--                   payable exactly once, so a double click cannot write two
--                   snapshots.
--
-- Money is BIGINT IDR; percent_bp is basis points of a percent (2500 =
-- 25.00%), so commission math never needs a float. A payout's commission and
-- total may be negative: a late refund for an earlier visit can land in a
-- period with no visits. gross_idr stays non-negative because it sums visits.
--
-- Additive only: no existing table is touched.

CREATE TABLE IF NOT EXISTS staff_comp (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    staff_id UUID NOT NULL REFERENCES staff(id) ON DELETE CASCADE,
    percent_bp INT NOT NULL DEFAULT 0 CHECK (percent_bp BETWEEN 0 AND 10000),
    fixed_idr BIGINT NOT NULL DEFAULT 0 CHECK (fixed_idr >= 0),
    cadence TEXT NOT NULL DEFAULT 'monthly' CHECK (cadence IN ('weekly', 'monthly')),
    commission_base TEXT NOT NULL DEFAULT 'list' CHECK (commission_base IN ('list', 'net')),
    effective_from DATE NOT NULL,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- EffectiveFor is a point lookup on (staff, date) newest-first; the history
-- read walks the same index backwards.
CREATE INDEX IF NOT EXISTS staff_comp_staff_effective_idx
    ON staff_comp (staff_id, effective_from DESC);

CREATE TABLE IF NOT EXISTS staff_payouts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    staff_id UUID NOT NULL REFERENCES staff(id) ON DELETE RESTRICT,
    period_kind TEXT NOT NULL CHECK (period_kind IN ('weekly', 'monthly')),
    period_start DATE NOT NULL,
    period_end DATE NOT NULL CHECK (period_end >= period_start),
    gross_idr BIGINT NOT NULL DEFAULT 0 CHECK (gross_idr >= 0),
    percent_bp INT NOT NULL DEFAULT 0 CHECK (percent_bp BETWEEN 0 AND 10000),
    fixed_idr BIGINT NOT NULL DEFAULT 0 CHECK (fixed_idr >= 0),
    commission_idr BIGINT NOT NULL DEFAULT 0,
    total_idr BIGINT NOT NULL DEFAULT 0,
    note TEXT NOT NULL DEFAULT '',
    paid_by UUID REFERENCES users(id) ON DELETE SET NULL,
    paid_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- RESTRICT above plus this key: a paid period survives, and one staff
    -- member cannot be marked paid twice for the same period.
    CONSTRAINT staff_payouts_period_key UNIQUE (staff_id, period_kind, period_start)
);

-- The paid badge (per staff) rides the unique key's index; the payout history
-- reads a date range across all staff, which this one serves.
CREATE INDEX IF NOT EXISTS staff_payouts_period_idx
    ON staff_payouts (period_start DESC, period_kind);

-- +goose Down
DROP TABLE IF EXISTS staff_payouts;
DROP TABLE IF EXISTS staff_comp;
