package repo

import (
	"context"
	"fmt"
	"time"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PayrollRepo answers the two period reads the payroll service composes into
// per-therapist rows. Both are WIB-bounded and payment-blind: accrual is the
// visit's service date (a completed booking), never the day cash arrived, and
// the refund side is the recorded date, so a late refund lands in its own
// period instead of rewriting the visit's. Neither query reads booking_fee_idr
// or distance_fee_idr: commission is for the treatment, not logistics.
type PayrollRepo struct{ pool *pgxpool.Pool }

func NewPayrollRepo(pool *pgxpool.Pool) *PayrollRepo { return &PayrollRepo{pool: pool} }

// PayrollVisitRow is one completed visit in the period with the two money
// snapshots the base rule needs. CustomerName is display-only (the expandable
// visit list); the money columns are the booking's frozen price/discount, not
// the current catalog.
type PayrollVisitRow struct {
	BookingID    uuid.UUID
	StaffID      uuid.UUID
	StaffName    string
	StartAt      time.Time
	CustomerName string
	ServiceName  string
	PriceIDR     int64
	DiscountIDR  int64
}

// PayrollRefundRow is one active refund recorded in the period, carrying its
// booking's price/discount so the service can cap attribution at the booking's
// base under the therapist's commission base. AmountIDR is the recorded refund;
// the service decides how much of it the period deducts.
type PayrollRefundRow struct {
	RefundID    uuid.UUID
	BookingID   uuid.UUID
	StaffID     uuid.UUID
	StaffName   string
	CreatedAt   time.Time
	AmountIDR   int64
	Reason      string
	PriceIDR    int64
	DiscountIDR int64
}

// payrollWindow is the half-open WIB range both reads share: from midnight WIB
// on the period's first day to midnight WIB on the day after its last. Dates
// arrive as YYYY-MM-DD strings ($1/$2), so the comparison is a sargable range
// on the timestamp column rather than a per-row timezone conversion.
//
// The explicit ::timestamp casts are load-bearing: `date AT TIME ZONE zone`
// resolves to the timestamptz → timestamp overload (it would read the date in
// the session zone and then render it in Jakarta), while a bare date has no
// `AT TIME ZONE` of the form we mean. Casting to timestamp first selects the
// "interpret this wall clock in Asia/Jakarta" overload, and `date + interval`
// keeps the upper bound a timestamp for the same reason.
const payrollWindow = `%[1]s >= (($1::date)::timestamp AT TIME ZONE 'Asia/Jakarta')
	  AND %[1]s < (($2::date + interval '1 day') AT TIME ZONE 'Asia/Jakarta')`

// CompletedVisits returns every completed visit whose service date falls inside
// [from, to] (WIB dates). The staff join is inner on purpose: a booking without
// a therapist has nobody to accrue to and is not a payroll row. Homecare visits
// are included — staff_id defines ownership — and the booking fee / transport
// fee are never selected.
func (r *PayrollRepo) CompletedVisits(ctx context.Context, from, to time.Time) ([]PayrollVisitRow, error) {
	q := `SELECT b.id, b.staff_id, COALESCE(st.name,''), b.start_at,
		COALESCE(NULLIF(u.full_name,''), NULLIF(b.guest_name,''), ''),
		COALESCE(sv.name,''), b.price_idr, b.discount_idr
		FROM bookings b
		JOIN staff st ON st.id = b.staff_id
		LEFT JOIN users u ON u.id = b.customer_id
		LEFT JOIN services sv ON sv.id = b.service_id
		WHERE b.status = 'completed' AND ` + fmt.Sprintf(payrollWindow, "b.start_at") + `
		ORDER BY b.staff_id, b.start_at, b.id`
	rows, err := r.pool.Query(ctx, q, wibDate(from), wibDate(to))
	if err != nil {
		return nil, fmt.Errorf("payroll visits: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[PayrollVisitRow])
	if err != nil {
		return nil, fmt.Errorf("payroll visits: %w", err)
	}
	return out, nil
}

// RefundsInRange returns the active (non-voided) refunds recorded inside
// [from, to] (WIB dates) with the booking's frozen price/discount attached.
//
// Two filters carry the accrual rule. Voided refunds are corrections, not money.
// The booking must also still be 'completed': payroll accrues on performed work
// in both directions, and a refund on a cancelled or no-show booking is a
// logistics reversal (the visit never happened), not a treatment reversal — it
// must not produce negative commission. Cancel-then-refund is a normal flow, so
// this predicate is what keeps that flow out of the payroll math. The staff join
// is inner for the same reason as CompletedVisits: a refund on a booking with no
// therapist has nobody to attribute it to.
func (r *PayrollRepo) RefundsInRange(ctx context.Context, from, to time.Time) ([]PayrollRefundRow, error) {
	q := `SELECT r.id, r.booking_id, b.staff_id, COALESCE(st.name,''), r.created_at,
		r.amount_idr, r.reason, b.price_idr, b.discount_idr
		FROM refunds r
		JOIN bookings b ON b.id = r.booking_id
		JOIN staff st ON st.id = b.staff_id
		WHERE r.voided_at IS NULL AND b.status = 'completed'
		  AND ` + fmt.Sprintf(payrollWindow, "r.created_at") + `
		ORDER BY r.created_at, r.id`
	rows, err := r.pool.Query(ctx, q, wibDate(from), wibDate(to))
	if err != nil {
		return nil, fmt.Errorf("payroll refunds: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[PayrollRefundRow])
	if err != nil {
		return nil, fmt.Errorf("payroll refunds: %w", err)
	}
	return out, nil
}

// wibDate renders the WIB calendar date of t as the YYYY-MM-DD string the
// period predicates cast. Callers pass period bounds already built in WIB
// (service.PayrollPeriodFor), so the clock they were given is the one meant;
// a caller that passes a UTC instant is asking for that instant's own date.
func wibDate(t time.Time) string { return t.Format("2006-01-02") }

// ListStaff returns every therapist for the rate form, ordered by name. Payroll
// is location-agnostic (a rate follows the person, not the branch), so unlike
// CatalogRepo.ListStaff this read spans every branch and keeps inactive staff:
// a former therapist's rate timeline still needs correcting. Only the three
// columns the form needs are read; domain.Staff carries LocationID but not a
// location name, so none is invented here.
func (r *PayrollRepo) ListStaff(ctx context.Context) ([]domain.Staff, error) {
	const q = `SELECT id, name, active FROM staff ORDER BY name, id`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("payroll staff: %w", err)
	}
	defer rows.Close()

	out := make([]domain.Staff, 0, 8)
	for rows.Next() {
		var s domain.Staff
		if err := rows.Scan(&s.ID, &s.Name, &s.Active); err != nil {
			return nil, fmt.Errorf("payroll staff: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("payroll staff: %w", err)
	}
	return out, nil
}
