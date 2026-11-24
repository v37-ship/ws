package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// StaffCompRepo is the append-only pay-rate timeline: a change is a new row
// with a later effective_from, so a past period always resolves to the rate
// that was in force then.
type StaffCompRepo struct{ pool *pgxpool.Pool }

func NewStaffCompRepo(pool *pgxpool.Pool) *StaffCompRepo { return &StaffCompRepo{pool: pool} }

const staffCompCols = `id, staff_id, percent_bp, fixed_idr, cadence, commission_base,
	effective_from, created_by, created_at`

// dateOnly folds a time to its calendar date in UTC midnight, the shape pgx
// scans a DATE back as. Callers may pass a time in any location; the date they
// meant is the one on their own clock, so the Date() parts are what survive.
func dateOnly(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

func scanStaffComp(row pgx.Row) (*domain.StaffComp, error) {
	var c domain.StaffComp
	if err := row.Scan(&c.ID, &c.StaffID, &c.PercentBp, &c.FixedIDR, &c.Cadence,
		&c.CommissionBase, &c.EffectiveFrom, &c.CreatedBy, &c.CreatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}

// Create appends one rate row. A supersede is a plain insert: the newest row
// whose effective_from is not after a period's start is the one that pays it.
func (r *StaffCompRepo) Create(ctx context.Context, c *domain.StaffComp) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	const q = `INSERT INTO staff_comp
			(id, staff_id, percent_bp, fixed_idr, cadence, commission_base, effective_from, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		RETURNING created_at`
	if err := r.pool.QueryRow(ctx, q, c.ID, c.StaffID, c.PercentBp, c.FixedIDR,
		c.Cadence, c.CommissionBase, dateOnly(c.EffectiveFrom), c.CreatedBy,
	).Scan(&c.CreatedAt); err != nil {
		return fmt.Errorf("create staff rate: %w", err)
	}
	return nil
}

// EffectiveFor resolves the rate in force at a period's start: the newest row
// with effective_from <= periodStart. A staff member with no rate row yet is a
// normal state (the payroll page flags the missing rate), so absence is
// (nil, nil); a driver error is returned wrapped.
func (r *StaffCompRepo) EffectiveFor(ctx context.Context, staffID uuid.UUID, periodStart time.Time) (*domain.StaffComp, error) {
	const q = `SELECT ` + staffCompCols + `
		FROM staff_comp
		WHERE staff_id = $1 AND effective_from <= $2
		ORDER BY effective_from DESC, created_at DESC, id DESC
		LIMIT 1`
	c, err := scanStaffComp(r.pool.QueryRow(ctx, q, staffID, dateOnly(periodStart)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("effective staff rate: %w", err)
	}
	return c, nil
}

// HistoryFor returns one staff member's whole rate timeline, newest first — the
// read the portal's rate history renders.
func (r *StaffCompRepo) HistoryFor(ctx context.Context, staffID uuid.UUID) ([]domain.StaffComp, error) {
	q := `SELECT ` + staffCompCols + `
		FROM staff_comp
		WHERE staff_id = $1
		ORDER BY effective_from DESC, created_at DESC, id DESC`
	rows, err := r.pool.Query(ctx, q, staffID)
	if err != nil {
		return nil, fmt.Errorf("staff rate history: %w", err)
	}
	defer rows.Close()

	out := make([]domain.StaffComp, 0, 4)
	for rows.Next() {
		c, err := scanStaffComp(rows)
		if err != nil {
			return nil, fmt.Errorf("staff rate history: %w", err)
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("staff rate history: %w", err)
	}
	return out, nil
}

// ListAll returns every rate row, grouped by staff and newest first within a
// staff member, so the rate management page can show the current row per
// therapist without a query per row.
func (r *StaffCompRepo) ListAll(ctx context.Context) ([]domain.StaffComp, error) {
	q := `SELECT ` + staffCompCols + `
		FROM staff_comp
		ORDER BY staff_id, effective_from DESC, created_at DESC, id DESC`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list staff rates: %w", err)
	}
	defer rows.Close()

	out := make([]domain.StaffComp, 0, 16)
	for rows.Next() {
		c, err := scanStaffComp(rows)
		if err != nil {
			return nil, fmt.Errorf("list staff rates: %w", err)
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list staff rates: %w", err)
	}
	return out, nil
}

// StaffPayoutRepo reads and writes the per-period paid snapshots. The database's
// partial unique (staff_id, period_kind, period_start) WHERE voided_at IS NULL
// is what makes "mark paid" happen exactly once per live period while a voided
// snapshot stays in history for the audit trail.
type StaffPayoutRepo struct{ pool *pgxpool.Pool }

func NewStaffPayoutRepo(pool *pgxpool.Pool) *StaffPayoutRepo { return &StaffPayoutRepo{pool: pool} }

const staffPayoutCols = `id, staff_id, period_kind, period_start, period_end,
	gross_idr, refund_idr, net_idr, percent_bp, fixed_idr, commission_idr, total_idr, note, paid_by, paid_at,
	voided_at, voided_by, void_reason`

func scanStaffPayout(row pgx.Row) (*domain.StaffPayout, error) {
	var p domain.StaffPayout
	if err := row.Scan(&p.ID, &p.StaffID, &p.PeriodKind, &p.PeriodStart,
		&p.PeriodEnd, &p.GrossIDR, &p.RefundIDR, &p.NetIDR, &p.PercentBp, &p.FixedIDR,
		&p.CommissionIDR, &p.TotalIDR, &p.Note, &p.PaidBy, &p.PaidAt,
		&p.VoidedAt, &p.VoidedBy, &p.VoidReason); err != nil {
		return nil, err
	}
	return &p, nil
}

// Create writes one paid snapshot. A duplicate live staff/kind/period is a real
// error (the partial unique index refuses the second live write), not a silent
// overwrite, so the caller can tell the second tab it was already marked paid.
// A zero PaidAt falls back to the database clock.
func (r *StaffPayoutRepo) Create(ctx context.Context, p *domain.StaffPayout) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	var paidAt *time.Time
	if !p.PaidAt.IsZero() {
		paidAt = &p.PaidAt
	}
	const q = `INSERT INTO staff_payouts
			(id, staff_id, period_kind, period_start, period_end, gross_idr, refund_idr,
			 net_idr, percent_bp, fixed_idr, commission_idr, total_idr, note, paid_by, paid_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,COALESCE($15, now()))
		RETURNING paid_at`
	if err := r.pool.QueryRow(ctx, q, p.ID, p.StaffID, p.PeriodKind,
		dateOnly(p.PeriodStart), dateOnly(p.PeriodEnd), p.GrossIDR, p.RefundIDR,
		p.NetIDR, p.PercentBp, p.FixedIDR, p.CommissionIDR, p.TotalIDR, p.Note,
		p.PaidBy, paidAt,
	).Scan(&p.PaidAt); err != nil {
		return fmt.Errorf("create staff payout: %w", err)
	}
	return nil
}

// ForPeriod returns the LIVE snapshot that marked one staff member's period
// paid, or (nil, nil) while the period is still unpaid — the portal's badge
// reads that absence as "not paid yet", so it is not an error. Voided rows are
// history: they never make a period paid again.
func (r *StaffPayoutRepo) ForPeriod(ctx context.Context, staffID uuid.UUID, kind string, periodStart time.Time) (*domain.StaffPayout, error) {
	const q = `SELECT ` + staffPayoutCols + `
		FROM staff_payouts
		WHERE staff_id = $1 AND period_kind = $2 AND period_start = $3
		  AND voided_at IS NULL`
	p, err := scanStaffPayout(r.pool.QueryRow(ctx, q, staffID, kind, dateOnly(periodStart)))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("staff payout for period: %w", err)
	}
	return p, nil
}

// HistoryFor returns every snapshot for one staff member's period, voided rows
// included, newest first. It is the audit read behind the page's voided list;
// ForPeriod is the live badge.
func (r *StaffPayoutRepo) HistoryFor(ctx context.Context, staffID uuid.UUID, kind string, periodStart time.Time) ([]domain.StaffPayout, error) {
	const q = `SELECT ` + staffPayoutCols + `
		FROM staff_payouts
		WHERE staff_id = $1 AND period_kind = $2 AND period_start = $3
		ORDER BY paid_at DESC, id DESC`
	rows, err := r.pool.Query(ctx, q, staffID, kind, dateOnly(periodStart))
	if err != nil {
		return nil, fmt.Errorf("staff payout history: %w", err)
	}
	defer rows.Close()

	out := make([]domain.StaffPayout, 0, 2)
	for rows.Next() {
		p, err := scanStaffPayout(rows)
		if err != nil {
			return nil, fmt.Errorf("staff payout history: %w", err)
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("staff payout history: %w", err)
	}
	return out, nil
}

// Void marks one snapshot voided with a required reason, atomically and only
// while it is still live. The row is never deleted or edited: it stays in
// history, stops counting as paid, and the partial unique index then lets the
// period be marked again. The bool reports whether this call actually voided
// it (false = already voided or absent), so a double submit is an inline
// message, not a second write.
func (r *StaffPayoutRepo) Void(ctx context.Context, id uuid.UUID, by uuid.UUID, reason string) (bool, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE staff_payouts
		SET voided_at = now(), voided_by = $2, void_reason = $3
		WHERE id = $1 AND voided_at IS NULL`, id, by, reason)
	if err != nil {
		return false, fmt.Errorf("void staff payout: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// ListFor returns the snapshots whose period starts inside [from, to],
// inclusive, newest period first. It is the payout-history read, so it includes
// voided rows and never filters by staff: the caller groups by StaffID and
// filters the live ones where the badge or a week count needs it.
func (r *StaffPayoutRepo) ListFor(ctx context.Context, from, to time.Time) ([]domain.StaffPayout, error) {
	const q = `SELECT ` + staffPayoutCols + `
		FROM staff_payouts
		WHERE period_start >= $1 AND period_start <= $2
		ORDER BY period_start DESC, paid_at DESC, staff_id`
	rows, err := r.pool.Query(ctx, q, dateOnly(from), dateOnly(to))
	if err != nil {
		return nil, fmt.Errorf("list staff payouts: %w", err)
	}
	defer rows.Close()

	out := make([]domain.StaffPayout, 0, 8)
	for rows.Next() {
		p, err := scanStaffPayout(rows)
		if err != nil {
			return nil, fmt.Errorf("list staff payouts: %w", err)
		}
		out = append(out, *p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list staff payouts: %w", err)
	}
	return out, nil
}
