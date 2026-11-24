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
--                   are copied in at mark-paid time and never edited, so a
--                   closed period cannot be rewritten by a later rate change or
--                   a late refund. A mistaken mark-paid is corrected by an
--                   ADJUSTMENT payout recorded for a later period, with a note
--                   saying what it fixes — the unique key means one staff
--                   member's period can never hold a second snapshot, so a
--                   double click cannot write two.
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
    commission_base TEXT NOT NULL DEFAULT 'net' CHECK (commission_base IN ('list', 'net')),
    effective_from DATE NOT NULL,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- EffectiveFor filters to one staff member's rows and reads newest-first, and
-- the UNIQUE key refuses a second row for the same staff + effective_from: the
-- rate form's duplicate check is a friendly pre-flight, but two concurrent
-- submits can both pass that read, and this index is what makes the timeline
-- unambiguous in the end (a duplicate would otherwise resolve by the
-- created_at tie-break). The DESC order is kept: Postgres accepts it on a
-- unique index, and it serves the newest-first scan directly.
CREATE UNIQUE INDEX IF NOT EXISTS staff_comp_staff_effective_idx
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
-- filters a period_start range with this one. Its paid_at/staff_id tie-breaks
-- are a sort of the filtered rows, which stay few (one per staff per period).
CREATE INDEX IF NOT EXISTS staff_payouts_period_idx
    ON staff_payouts (period_start DESC, period_kind);

-- +goose Down
DROP TABLE IF EXISTS staff_payouts;
DROP TABLE IF EXISTS staff_comp;
