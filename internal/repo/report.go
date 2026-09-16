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

// MonthlyLines returns paid payments in the given YYYY-MM month.
func (r *ReportRepo) MonthlyLines(ctx context.Context, month string, locationID uuid.UUID) ([]domain.ReportLine, error) {
	q := fmt.Sprintf(`SELECT p.id, p.booking_id, p.amount_idr, p.payment_method, p.payment_purpose, p.paid_at,
		b.start_at, b.customer_id, COALESCE(u.full_name,''), b.service_id, COALESCE(sv.name,''),
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

// MonthlyRefunds returns active (non-voided) refunds recorded in the given YYYY-MM month.
func (r *ReportRepo) MonthlyRefunds(ctx context.Context, month string, locationID uuid.UUID) ([]domain.ReportRefund, error) {
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
