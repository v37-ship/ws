package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ReportRepo serves monthly revenue reporting plus the refunds ledger.
// Money is counted cash-basis in Asia/Jakarta: payments by paid_at month,
// refunds by recorded (created_at) month.
type ReportRepo struct{ pool *pgxpool.Pool }

func NewReportRepo(pool *pgxpool.Pool) *ReportRepo { return &ReportRepo{pool: pool} }

const jakartaMonth = `date_trunc('month', %s AT TIME ZONE 'Asia/Jakarta') = $1::date`

// jakartaDay is the daily reporting bucket: a timestamptz becomes a WIB
// calendar day, never a UTC one. The daily view counts visits and payments
// through this same predicate, so a payment at 00:30 WIB on the 2nd (17:30 UTC
// on the 1st) belongs to the 2nd.
const jakartaDay = `date(%s AT TIME ZONE 'Asia/Jakarta') = $1::date`

// DailyCounts returns one WIB day's visit mix. Walk-ins count bookings whose
// source is 'walkin' whatever their status; completed/no-show/cancelled count
// statuses. It carries no money: payments are bucketed by paid_at, which is a
// different day than the visit date.
func (r *ReportRepo) DailyCounts(ctx context.Context, date string, locationID uuid.UUID) (domain.DailySummary, error) {
	q := fmt.Sprintf(`SELECT
		count(*) FILTER (WHERE b.status = 'completed'),
		count(*) FILTER (WHERE b.source = 'walkin'),
		count(*) FILTER (WHERE b.status = 'no_show'),
		count(*) FILTER (WHERE b.status = 'cancelled')
		FROM bookings b
		WHERE `+jakartaDay, "b.start_at")
	args := []any{date}
	if locationID != uuid.Nil {
		q += " AND b.location_id = $2"
		args = append(args, locationID)
	}
	sum := domain.DailySummary{Date: date}
	if err := r.pool.QueryRow(ctx, q, args...).Scan(
		&sum.Completed, &sum.WalkIns, &sum.NoShows, &sum.Cancelled,
	); err != nil {
		return domain.DailySummary{}, err
	}
	return sum, nil
}

// DailyPayments sums paid money collected on a WIB day, split cash / QRIS.
// Anything that is not cash falls into the QRIS side, matching MonthlyReport's
// method split. Paid_at is the bucket: a payment belongs to the day the money
// arrived, not the day of the visit.
func (r *ReportRepo) DailyPayments(ctx context.Context, date string, locationID uuid.UUID) (cash, qris int64, err error) {
	q := fmt.Sprintf(`SELECT
		COALESCE(SUM(p.amount_idr) FILTER (WHERE p.payment_method = 'cash'), 0),
		COALESCE(SUM(p.amount_idr) FILTER (WHERE p.payment_method <> 'cash'), 0)
		FROM payments p
		JOIN bookings b ON b.id = p.booking_id
		WHERE p.status = 'paid' AND p.paid_at IS NOT NULL AND `+jakartaDay, "p.paid_at")
	args := []any{date}
	if locationID != uuid.Nil {
		q += " AND b.location_id = $2"
		args = append(args, locationID)
	}
	err = r.pool.QueryRow(ctx, q, args...).Scan(&cash, &qris)
	return cash, qris, err
}

// DailyUpcoming returns the WIB day's confirmed rows that start after `at`
// (today's remaining visits), soonest first. `at` is a parameter rather than
// now() so the read is testable at a fixed clock; the service passes time.Now.
func (r *ReportRepo) DailyUpcoming(ctx context.Context, date string, at time.Time, locationID uuid.UUID) ([]domain.ReportLine, error) {
	q := fmt.Sprintf(`SELECT b.id, b.start_at, b.customer_id,
		COALESCE(NULLIF(b.guest_name,''), u.full_name,''),
		b.service_id, COALESCE(sv.name,''),
		b.staff_id, COALESCE(st.name,''),
		b.status
		FROM bookings b
		LEFT JOIN users u ON u.id = b.customer_id
		LEFT JOIN services sv ON sv.id = b.service_id
		LEFT JOIN staff st ON st.id = b.staff_id
		WHERE b.status = 'confirmed' AND b.start_at > $2 AND `+jakartaDay, "b.start_at")
	args := []any{date, at}
	if locationID != uuid.Nil {
		q += " AND b.location_id = $3"
		args = append(args, locationID)
	}
	q += " ORDER BY b.start_at ASC, b.id ASC LIMIT 50"
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ReportLine
	for rows.Next() {
		var ln domain.ReportLine
		if err := rows.Scan(
			&ln.BookingID, &ln.StartAt, &ln.CustomerID, &ln.CustomerName,
			&ln.ServiceID, &ln.ServiceName,
			&ln.StaffID, &ln.StaffName,
			&ln.Status,
		); err != nil {
			return nil, err
		}
		out = append(out, ln)
	}
	return out, rows.Err()
}

// MonthlyLines returns paid payments in the given YYYY-MM month.
func (r *ReportRepo) MonthlyLines(ctx context.Context, month string, locationID uuid.UUID) ([]domain.ReportLine, error) {
	return r.monthlyLines(ctx, month, locationID, 0)
}

// MonthlyLinesThrough is MonthlyLines cut at a WIB day-of-month, inclusive:
// only payments whose Jakarta calendar day is <= throughDay are returned.
// throughDay <= 0 means the whole month. The month-to-date delta uses this, so
// the cutoff is the same WIB day bucket the rest of the report counts in.
func (r *ReportRepo) MonthlyLinesThrough(ctx context.Context, month string, locationID uuid.UUID, throughDay int) ([]domain.ReportLine, error) {
	return r.monthlyLines(ctx, month, locationID, throughDay)
}

func (r *ReportRepo) monthlyLines(ctx context.Context, month string, locationID uuid.UUID, throughDay int) ([]domain.ReportLine, error) {
	q := fmt.Sprintf(`SELECT p.id, p.booking_id, p.amount_idr, p.payment_method, p.payment_purpose, p.paid_at,
		b.start_at, b.customer_id, COALESCE(NULLIF(b.guest_name,''), u.full_name,''), b.service_id, COALESCE(sv.name,''),
		b.staff_id, COALESCE(st.name,''),
		COALESCE(l.name,''), l.id, b.status
		FROM payments p
		JOIN bookings b ON b.id = p.booking_id
		LEFT JOIN users u ON u.id = b.customer_id
		LEFT JOIN services sv ON sv.id = b.service_id
		LEFT JOIN staff st ON st.id = b.staff_id
		LEFT JOIN locations l ON l.id = b.location_id
		WHERE p.status='paid' AND p.paid_at IS NOT NULL AND `+jakartaMonth, "p.paid_at")
	args := []any{month + "-01"}
	if locationID != uuid.Nil {
		q += " AND b.location_id = $2"
		args = append(args, locationID)
	}
	if throughDay > 0 {
		q += fmt.Sprintf(" AND date_part('day', p.paid_at AT TIME ZONE 'Asia/Jakarta') <= $%d", len(args)+1)
		args = append(args, throughDay)
	}
	q += " ORDER BY p.paid_at ASC"
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ReportLine
	for rows.Next() {
		var ln domain.ReportLine
		if err := rows.Scan(
			&ln.PaymentID, &ln.BookingID, &ln.AmountIDR, &ln.PaymentMethod, &ln.Purpose,
			&ln.PaidAt, &ln.StartAt, &ln.CustomerID, &ln.CustomerName, &ln.ServiceID, &ln.ServiceName,
			&ln.StaffID, &ln.StaffName,
			&ln.LocationName, &ln.LocationID, &ln.Status,
		); err != nil {
			return nil, err
		}
		out = append(out, ln)
	}
	return out, rows.Err()
}

// DemandCounts returns the month's business-health figures in one pass over
// bookings (plus the booking's single payment row, joined one-to-one). Two date
// bases, matching the page: the funnel counts created_at, the visit mix counts
// start_at, and paid figures count paid_at — all compared as Asia/Jakarta
// months. The WHERE clause keeps the scan to rows that belong to at least one of
// those buckets. No money is summed except the discount and transport snapshots
// the booking froze; gross paid comes from the month summary, never from here.
func (r *ReportRepo) DemandCounts(ctx context.Context, month string, locationID uuid.UUID) (domain.ReportDemand, error) {
	created := fmt.Sprintf(jakartaMonth, "b.created_at")
	visit := fmt.Sprintf(jakartaMonth, "b.start_at")
	paid := fmt.Sprintf(jakartaMonth, "p.paid_at")
	q := `SELECT
		count(*) FILTER (WHERE ` + created + `),
		count(*) FILTER (WHERE p.status = 'paid' AND p.paid_at IS NOT NULL AND ` + paid + `),
		count(*) FILTER (WHERE ` + created + ` AND b.status = 'cancelled'),
		count(*) FILTER (WHERE ` + created + ` AND b.status = 'no_show'),
		count(*) FILTER (WHERE ` + visit + `),
		count(*) FILTER (WHERE ` + visit + ` AND b.source = 'web'),
		count(*) FILTER (WHERE ` + visit + ` AND b.source = 'walkin'),
		count(*) FILTER (WHERE ` + visit + ` AND b.source = 'whatsapp'),
		count(*) FILTER (WHERE ` + visit + ` AND b.source = 'phone'),
		count(*) FILTER (WHERE ` + visit + ` AND b.place = 'homecare'),
		count(*) FILTER (WHERE ` + visit + ` AND b.status IN ('completed','cancelled','no_show')),
		count(*) FILTER (WHERE ` + visit + ` AND b.status = 'no_show'),
		COALESCE(SUM(b.discount_idr) FILTER (WHERE ` + visit + `), 0),
		COALESCE(SUM(b.distance_fee_idr) FILTER (WHERE ` + visit + `), 0)
		FROM bookings b
		LEFT JOIN payments p ON p.booking_id = b.id
		WHERE (` + created + ` OR ` + visit + `
			OR (p.status = 'paid' AND p.paid_at IS NOT NULL AND ` + paid + `))`
	args := []any{month + "-01"}
	if locationID != uuid.Nil {
		q += " AND b.location_id = $2"
		args = append(args, locationID)
	}
	var d domain.ReportDemand
	err := r.pool.QueryRow(ctx, q, args...).Scan(
		&d.BookingsCreated, &d.BookingsPaid, &d.Cancelled, &d.NoShows,
		&d.Visits, &d.SourceWeb, &d.SourceWalkin, &d.SourceWhatsapp, &d.SourcePhone,
		&d.HomecareVisits, &d.TerminalVisits, &d.NoShowVisits,
		&d.PromoDiscIDR, &d.TransportIDR,
	)
	return d, err
}

// MonthlyRefunds returns active (non-voided) refunds recorded in the given YYYY-MM month.
func (r *ReportRepo) MonthlyRefunds(ctx context.Context, month string, locationID uuid.UUID) ([]domain.ReportRefund, error) {
	return r.monthlyRefunds(ctx, month, locationID, 0)
}

// MonthlyRefundsThrough is MonthlyRefunds cut at a WIB day-of-month, inclusive
// (by created_at, the same bucket the monthly report deducts in). throughDay
// <= 0 means the whole month.
func (r *ReportRepo) MonthlyRefundsThrough(ctx context.Context, month string, locationID uuid.UUID, throughDay int) ([]domain.ReportRefund, error) {
	return r.monthlyRefunds(ctx, month, locationID, throughDay)
}

func (r *ReportRepo) monthlyRefunds(ctx context.Context, month string, locationID uuid.UUID, throughDay int) ([]domain.ReportRefund, error) {
	q := fmt.Sprintf(`SELECT r.id, r.booking_id, r.amount_idr, r.reason, r.created_by_id,
		COALESCE(maker.full_name,''), r.created_at,
		COALESCE(u.full_name,''), COALESCE(sv.name,''),
		b.staff_id, COALESCE(st.name,''),
		COALESCE(l.name,''), l.id
		FROM refunds r
		JOIN bookings b ON b.id = r.booking_id
		LEFT JOIN users u ON u.id = b.customer_id
		LEFT JOIN services sv ON sv.id = b.service_id
		LEFT JOIN staff st ON st.id = b.staff_id
		LEFT JOIN locations l ON l.id = b.location_id
		LEFT JOIN users maker ON maker.id = r.created_by_id
		WHERE r.voided_at IS NULL AND `+jakartaMonth, "r.created_at")
	args := []any{month + "-01"}
	if locationID != uuid.Nil {
		q += " AND b.location_id = $2"
		args = append(args, locationID)
	}
	if throughDay > 0 {
		q += fmt.Sprintf(" AND date_part('day', r.created_at AT TIME ZONE 'Asia/Jakarta') <= $%d", len(args)+1)
		args = append(args, throughDay)
	}
	q += " ORDER BY r.created_at ASC"
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ReportRefund
	for rows.Next() {
		var rf domain.ReportRefund
		if err := rows.Scan(
			&rf.ID, &rf.BookingID, &rf.AmountIDR, &rf.Reason, &rf.CreatedByID,
			&rf.CreatedBy, &rf.CreatedAt,
			&rf.CustomerName, &rf.ServiceName,
			&rf.StaffID, &rf.StaffName,
			&rf.LocationName, &rf.LocationID,
		); err != nil {
			return nil, err
		}
		out = append(out, rf)
	}
	return out, rows.Err()
}

// AvailableMonths returns YYYY-MM months (desc) that have paid payments or
// recorded refunds, for the jump-to-month dropdown. Bounded by limit.
func (r *ReportRepo) AvailableMonths(ctx context.Context, locationID uuid.UUID, limit int) ([]string, error) {
	if limit <= 0 || limit > 60 {
		limit = 24
	}
	args := []any{}
	locFilterP := ""
	locFilterR := ""
	if locationID != uuid.Nil {
		args = append(args, locationID)
		locFilterP = " AND b.location_id = $1"
		locFilterR = " AND b.location_id = $1"
	}
	q := `SELECT m FROM (
		SELECT DISTINCT date_trunc('month', p.paid_at AT TIME ZONE 'Asia/Jakarta') AS m
		FROM payments p JOIN bookings b ON b.id = p.booking_id
		WHERE p.status='paid' AND p.paid_at IS NOT NULL` + locFilterP + `
		UNION
		SELECT DISTINCT date_trunc('month', r.created_at AT TIME ZONE 'Asia/Jakarta') AS m
		FROM refunds r JOIN bookings b ON b.id = r.booking_id
		WHERE r.voided_at IS NULL` + locFilterR + `
	) t ORDER BY m DESC LIMIT ` + fmt.Sprintf("%d", limit)
	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var when time.Time
		if err := rows.Scan(&when); err != nil {
			return nil, err
		}
		out = append(out, when.Format("2006-01"))
	}
	return out, rows.Err()
}

// RefundsForBooking lists refunds for one booking, newest first,
// including voided rows (shown struck-through as the correction trail).
func (r *ReportRepo) RefundsForBooking(ctx context.Context, bookingID uuid.UUID) ([]domain.Refund, error) {
	const q = `SELECT r.id, r.booking_id, r.amount_idr, r.reason, r.created_by_id,
		COALESCE(maker.full_name,''), r.created_at,
		r.voided_at, r.voided_by_id, COALESCE(voider.full_name,''), r.void_reason
		FROM refunds r
		LEFT JOIN users maker ON maker.id = r.created_by_id
		LEFT JOIN users voider ON voider.id = r.voided_by_id
		WHERE r.booking_id=$1 ORDER BY r.created_at DESC`
	rows, err := r.pool.Query(ctx, q, bookingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Refund
	for rows.Next() {
		var rf domain.Refund
		if err := rows.Scan(&rf.ID, &rf.BookingID, &rf.AmountIDR, &rf.Reason,
			&rf.CreatedByID, &rf.CreatedBy, &rf.CreatedAt,
			&rf.VoidedAt, &rf.VoidedByID, &rf.VoidedBy, &rf.VoidReason); err != nil {
			return nil, err
		}
		out = append(out, rf)
	}
	return out, rows.Err()
}

// RefundByID returns one refund row with its void state.
func (r *ReportRepo) RefundByID(ctx context.Context, refundID uuid.UUID) (*domain.Refund, error) {
	const q = `SELECT r.id, r.booking_id, r.amount_idr, r.reason, r.created_by_id,
		COALESCE(maker.full_name,''), r.created_at,
		r.voided_at, r.voided_by_id, COALESCE(voider.full_name,''), r.void_reason
		FROM refunds r
		LEFT JOIN users maker ON maker.id = r.created_by_id
		LEFT JOIN users voider ON voider.id = r.voided_by_id
		WHERE r.id=$1`
	var rf domain.Refund
	err := r.pool.QueryRow(ctx, q, refundID).Scan(&rf.ID, &rf.BookingID, &rf.AmountIDR, &rf.Reason,
		&rf.CreatedByID, &rf.CreatedBy, &rf.CreatedAt,
		&rf.VoidedAt, &rf.VoidedByID, &rf.VoidedBy, &rf.VoidReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &rf, nil
}

// VoidRefund marks a refund voided; totals exclude it from that moment on.
// Returns ErrNotFound when the row is missing or already voided.
func (r *ReportRepo) VoidRefund(ctx context.Context, refundID uuid.UUID, actorID *uuid.UUID, reason string) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE refunds SET voided_at=now(), voided_by_id=$2, void_reason=$3
		WHERE id=$1 AND voided_at IS NULL`, refundID, actorID, strings.TrimSpace(reason))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RefundedTotal sums active (non-voided) refunds recorded for a booking.
func (r *ReportRepo) RefundedTotal(ctx context.Context, bookingID uuid.UUID) (int64, error) {
	var total int64
	err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount_idr),0) FROM refunds WHERE booking_id=$1 AND voided_at IS NULL`, bookingID).Scan(&total)
	return total, err
}

// CreateRefund inserts a refund row and returns it.
func (r *ReportRepo) CreateRefund(ctx context.Context, bookingID uuid.UUID, amount int64, reason string, actorID *uuid.UUID) (*domain.Refund, error) {
	rf := &domain.Refund{
		ID: uuid.New(), BookingID: bookingID, AmountIDR: amount,
		Reason: strings.TrimSpace(reason), CreatedByID: actorID,
	}
	const q = `INSERT INTO refunds (id, booking_id, amount_idr, reason, created_by_id)
		VALUES ($1,$2,$3,$4,$5) RETURNING created_at`
	if err := r.pool.QueryRow(ctx, q,
		rf.ID, rf.BookingID, rf.AmountIDR, rf.Reason, rf.CreatedByID).Scan(&rf.CreatedAt); err != nil {
		return nil, err
	}
	return rf, nil
}
