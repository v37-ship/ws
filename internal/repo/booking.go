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

type BookingRepo struct{ pool *pgxpool.Pool }

func NewBookingRepo(pool *pgxpool.Pool) *BookingRepo { return &BookingRepo{pool: pool} }

func (r *BookingRepo) Create(ctx context.Context, b *domain.Booking) error {
	if b.ID == uuid.Nil {
		b.ID = uuid.New()
	}
	if b.Source == "" {
		b.Source = "web"
	}
	const q = `INSERT INTO bookings (
		id, customer_id, location_id, service_id, staff_id, start_at, end_at, status,
		price_idr, booking_fee_idr, hold_expires_at, notes, source, created_by_id, updated_by_id
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
	RETURNING created_at, updated_at`
	return r.pool.QueryRow(ctx, q,
		b.ID, b.CustomerID, b.LocationID, b.ServiceID, b.StaffID,
		b.StartAt, b.EndAt, b.Status, b.PriceIDR, b.BookingFeeIDR,
		b.HoldExpiresAt, b.Notes, b.Source, b.CreatedByID, b.UpdatedByID,
	).Scan(&b.CreatedAt, &b.UpdatedAt)
}

func (r *BookingRepo) ByID(ctx context.Context, id uuid.UUID) (*domain.Booking, error) {
	const q = `SELECT b.id, b.customer_id, b.location_id, b.service_id, b.staff_id, b.start_at, b.end_at,
		b.status, b.price_idr, b.booking_fee_idr, b.hold_expires_at, b.notes, b.source,
		b.created_by_id, b.updated_by_id, b.created_at, b.updated_at,
		COALESCE(u.full_name,''), COALESCE(u.phone,''), COALESCE(u.email,''),
		COALESCE(sv.name,''), COALESCE(st.name,''), COALESCE(l.name,''),
		COALESCE(u.baby_name,''), COALESCE(u.mom_condition,''),
		COALESCE(p.payment_method, 'qris'), COALESCE(p.status, 'pending')
		FROM bookings b
		LEFT JOIN users u ON u.id = b.customer_id
		LEFT JOIN services sv ON sv.id = b.service_id
		LEFT JOIN staff st ON st.id = b.staff_id
		LEFT JOIN locations l ON l.id = b.location_id
		LEFT JOIN payments p ON p.booking_id = b.id
		WHERE b.id=$1`
	var b domain.Booking
	err := r.pool.QueryRow(ctx, q, id).Scan(
		&b.ID, &b.CustomerID, &b.LocationID, &b.ServiceID, &b.StaffID, &b.StartAt, &b.EndAt,
		&b.Status, &b.PriceIDR, &b.BookingFeeIDR, &b.HoldExpiresAt, &b.Notes, &b.Source,
		&b.CreatedByID, &b.UpdatedByID, &b.CreatedAt, &b.UpdatedAt,
		&b.CustomerName, &b.CustomerPhone, &b.CustomerEmail,
		&b.ServiceName, &b.StaffName, &b.LocationName,
		&b.BabyName, &b.MomCondition,
		&b.PaymentMethod, &b.PaymentStatus,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

func (r *BookingRepo) ListByCustomer(ctx context.Context, customerID uuid.UUID) ([]domain.Booking, error) {
	const q = `SELECT b.id, b.customer_id, b.location_id, b.service_id, b.staff_id, b.start_at, b.end_at,
		b.status, b.price_idr, b.booking_fee_idr, b.hold_expires_at, b.notes, b.source,
		b.created_by_id, b.updated_by_id, b.created_at, b.updated_at,
		COALESCE(u.full_name,''), COALESCE(u.phone,''), COALESCE(u.email,''),
		COALESCE(sv.name,''), COALESCE(st.name,''), COALESCE(l.name,''),
		COALESCE(u.baby_name,''), COALESCE(u.mom_condition,''),
		COALESCE(p.payment_method, 'qris'), COALESCE(p.status, 'pending')
		FROM bookings b
		LEFT JOIN users u ON u.id = b.customer_id
		LEFT JOIN services sv ON sv.id = b.service_id
		LEFT JOIN staff st ON st.id = b.staff_id
		LEFT JOIN locations l ON l.id = b.location_id
		LEFT JOIN payments p ON p.booking_id = b.id
		WHERE b.customer_id=$1
		ORDER BY b.start_at DESC`
	return r.scanList(ctx, q, customerID)
}

func (r *BookingRepo) ListUpcoming(ctx context.Context, limit int) ([]domain.Booking, error) {
	const q = `SELECT b.id, b.customer_id, b.location_id, b.service_id, b.staff_id, b.start_at, b.end_at,
		b.status, b.price_idr, b.booking_fee_idr, b.hold_expires_at, b.notes, b.source,
		b.created_by_id, b.updated_by_id, b.created_at, b.updated_at,
		COALESCE(u.full_name,''), COALESCE(u.phone,''), COALESCE(u.email,''),
		COALESCE(sv.name,''), COALESCE(st.name,''), COALESCE(l.name,''),
		COALESCE(u.baby_name,''), COALESCE(u.mom_condition,''),
		COALESCE(p.payment_method, 'qris'), COALESCE(p.status, 'pending')
		FROM bookings b
		LEFT JOIN users u ON u.id = b.customer_id
		LEFT JOIN services sv ON sv.id = b.service_id
		LEFT JOIN staff st ON st.id = b.staff_id
		LEFT JOIN locations l ON l.id = b.location_id
		LEFT JOIN payments p ON p.booking_id = b.id
		WHERE b.start_at >= now() - interval '1 day'
		ORDER BY b.start_at ASC
		LIMIT $1`
	return r.scanList(ctx, q, limit)
}

type FilterParams struct {
	Status     domain.BookingStatus
	Date       string // YYYY-MM-DD
	LocationID uuid.UUID
	StaffID    uuid.UUID
	Search     string
	Limit      int
}

func (r *BookingRepo) ListFiltered(ctx context.Context, f FilterParams) ([]domain.Booking, error) {
	var sb strings.Builder
	sb.WriteString(`SELECT b.id, b.customer_id, b.location_id, b.service_id, b.staff_id, b.start_at, b.end_at,
		b.status, b.price_idr, b.booking_fee_idr, b.hold_expires_at, b.notes, b.source,
		b.created_by_id, b.updated_by_id, b.created_at, b.updated_at,
		COALESCE(u.full_name,''), COALESCE(u.phone,''), COALESCE(u.email,''),
		COALESCE(sv.name,''), COALESCE(st.name,''), COALESCE(l.name,''),
		COALESCE(u.baby_name,''), COALESCE(u.mom_condition,''),
		COALESCE(p.payment_method, 'qris'), COALESCE(p.status, 'pending')
		FROM bookings b
		LEFT JOIN users u ON u.id = b.customer_id
		LEFT JOIN services sv ON sv.id = b.service_id
		LEFT JOIN staff st ON st.id = b.staff_id
		LEFT JOIN locations l ON l.id = b.location_id
		LEFT JOIN payments p ON p.booking_id = b.id
		WHERE 1=1`)

	var args []any
	idx := 1

	if f.Status != "" {
		sb.WriteString(fmt.Sprintf(" AND b.status = $%d", idx))
		args = append(args, f.Status)
		idx++
	}
	if f.LocationID != uuid.Nil {
		sb.WriteString(fmt.Sprintf(" AND b.location_id = $%d", idx))
		args = append(args, f.LocationID)
		idx++
	}
	if f.StaffID != uuid.Nil {
		sb.WriteString(fmt.Sprintf(" AND b.staff_id = $%d", idx))
		args = append(args, f.StaffID)
		idx++
	}
	if f.Date != "" {
		sb.WriteString(fmt.Sprintf(" AND date(b.start_at AT TIME ZONE 'Asia/Jakarta') = $%d::date", idx))
		args = append(args, f.Date)
		idx++
	}
	if f.Search != "" {
		like := "%" + strings.ToLower(f.Search) + "%"
		sb.WriteString(fmt.Sprintf(" AND (lower(u.full_name) LIKE $%d OR u.phone LIKE $%d OR lower(u.email) LIKE $%d OR lower(u.baby_name) LIKE $%d)", idx, idx, idx, idx))
		args = append(args, like)
		idx++
	}

	sb.WriteString(" ORDER BY b.start_at ASC")
	if f.Limit > 0 {
		sb.WriteString(fmt.Sprintf(" LIMIT $%d", idx))
		args = append(args, f.Limit)
	}

	rows, err := r.pool.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Booking
	for rows.Next() {
		var b domain.Booking
		if err := rows.Scan(
			&b.ID, &b.CustomerID, &b.LocationID, &b.ServiceID, &b.StaffID, &b.StartAt, &b.EndAt,
			&b.Status, &b.PriceIDR, &b.BookingFeeIDR, &b.HoldExpiresAt, &b.Notes, &b.Source,
			&b.CreatedByID, &b.UpdatedByID, &b.CreatedAt, &b.UpdatedAt,
			&b.CustomerName, &b.CustomerPhone, &b.CustomerEmail,
			&b.ServiceName, &b.StaffName, &b.LocationName,
			&b.BabyName, &b.MomCondition,
			&b.PaymentMethod, &b.PaymentStatus,
		); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *BookingRepo) scanList(ctx context.Context, q string, arg any) ([]domain.Booking, error) {
	rows, err := r.pool.Query(ctx, q, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Booking
	for rows.Next() {
		var b domain.Booking
		if err := rows.Scan(
			&b.ID, &b.CustomerID, &b.LocationID, &b.ServiceID, &b.StaffID, &b.StartAt, &b.EndAt,
			&b.Status, &b.PriceIDR, &b.BookingFeeIDR, &b.HoldExpiresAt, &b.Notes, &b.Source,
			&b.CreatedByID, &b.UpdatedByID, &b.CreatedAt, &b.UpdatedAt,
			&b.CustomerName, &b.CustomerPhone, &b.CustomerEmail,
			&b.ServiceName, &b.StaffName, &b.LocationName,
			&b.BabyName, &b.MomCondition,
			&b.PaymentMethod, &b.PaymentStatus,
		); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (r *BookingRepo) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.BookingStatus) error {
	_, err := r.pool.Exec(ctx, `UPDATE bookings SET status=$2, updated_at=now() WHERE id=$1`, id, status)
	return err
}

func (r *BookingRepo) UpdateStatusWithLog(ctx context.Context, id uuid.UUID, status domain.BookingStatus, actorID *uuid.UUID, note string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `UPDATE bookings SET status=$2, updated_by_id=$3, updated_at=now() WHERE id=$1`, id, status, actorID)
	if err != nil {
		return err
	}

	action := "Status changed to " + string(status)
	_, err = tx.Exec(ctx, `INSERT INTO booking_logs (booking_id, user_id, action, notes) VALUES ($1,$2,$3,$4)`,
		id, actorID, action, note)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *BookingRepo) RescheduleWithLog(ctx context.Context, id uuid.UUID, staffID uuid.UUID, start, end time.Time, actorID *uuid.UUID, note string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		UPDATE bookings SET staff_id=$2, start_at=$3, end_at=$4, updated_by_id=$5, updated_at=now()
		WHERE id=$1`, id, staffID, start, end, actorID)
	if err != nil {
		return err
	}

	action := "Rescheduled appointment"
	_, err = tx.Exec(ctx, `INSERT INTO booking_logs (booking_id, user_id, action, notes) VALUES ($1,$2,$3,$4)`,
		id, actorID, action, note)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (r *BookingRepo) LogActivity(ctx context.Context, bookingID uuid.UUID, actorID *uuid.UUID, action, notes string) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO booking_logs (booking_id, user_id, action, notes) VALUES ($1,$2,$3,$4)`,
		bookingID, actorID, action, notes)
	return err
}

func (r *BookingRepo) ListLogs(ctx context.Context, bookingID uuid.UUID) ([]domain.BookingLog, error) {
	const q = `SELECT l.id, l.booking_id, l.user_id, COALESCE(u.full_name, 'System'), l.action, l.notes, l.created_at
		FROM booking_logs l
		LEFT JOIN users u ON u.id = l.user_id
		WHERE l.booking_id = $1
		ORDER BY l.created_at DESC`
	rows, err := r.pool.Query(ctx, q, bookingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.BookingLog
	for rows.Next() {
		var l domain.BookingLog
		if err := rows.Scan(&l.ID, &l.BookingID, &l.UserID, &l.UserName, &l.Action, &l.Notes, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r *BookingRepo) ExpireHolds(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE bookings SET status='expired', updated_at=now()
		WHERE status='pending_payment' AND hold_expires_at IS NOT NULL AND hold_expires_at < now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

type PaymentRepo struct{ pool *pgxpool.Pool }

func NewPaymentRepo(pool *pgxpool.Pool) *PaymentRepo { return &PaymentRepo{pool: pool} }

func (r *PaymentRepo) Create(ctx context.Context, p *domain.Payment) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	if p.PaymentMethod == "" {
		p.PaymentMethod = domain.PaymentMethodQRIS
	}
	if p.PaymentPurpose == "" {
		p.PaymentPurpose = domain.PaymentPurposeFull
	}
	const q = `INSERT INTO payments (id, booking_id, order_id, amount_idr, status, payment_method, payment_purpose, qr_string, midtrans_trans_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING created_at, updated_at`
	return r.pool.QueryRow(ctx, q,
		p.ID, p.BookingID, p.OrderID, p.AmountIDR, p.Status,
		p.PaymentMethod, p.PaymentPurpose, p.QRString, p.MidtransTransID,
	).Scan(&p.CreatedAt, &p.UpdatedAt)
}

func (r *PaymentRepo) CreateCashPayment(ctx context.Context, p *domain.Payment) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	now := time.Now()
	p.PaymentMethod = domain.PaymentMethodCash
	p.Status = domain.PaymentPaid
	p.PaidAt = &now
	if p.PaymentPurpose == "" {
		p.PaymentPurpose = domain.PaymentPurposeService
	}
	const q = `INSERT INTO payments (id, booking_id, order_id, amount_idr, status, payment_method, payment_purpose, paid_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING created_at, updated_at`
	return r.pool.QueryRow(ctx, q,
		p.ID, p.BookingID, p.OrderID, p.AmountIDR, p.Status,
		p.PaymentMethod, p.PaymentPurpose, p.PaidAt,
	).Scan(&p.CreatedAt, &p.UpdatedAt)
}

func (r *PaymentRepo) ByOrderID(ctx context.Context, orderID string) (*domain.Payment, error) {
	const q = `SELECT id, booking_id, order_id, amount_idr, status, payment_method, payment_purpose, qr_string, midtrans_trans_id, raw_notification, created_at, updated_at, paid_at
		FROM payments WHERE order_id=$1`
	var p domain.Payment
	err := r.pool.QueryRow(ctx, q, orderID).Scan(
		&p.ID, &p.BookingID, &p.OrderID, &p.AmountIDR, &p.Status,
		&p.PaymentMethod, &p.PaymentPurpose, &p.QRString, &p.MidtransTransID, &p.RawNotification,
		&p.CreatedAt, &p.UpdatedAt, &p.PaidAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *PaymentRepo) ByBookingID(ctx context.Context, bookingID uuid.UUID) (*domain.Payment, error) {
	const q = `SELECT id, booking_id, order_id, amount_idr, status, payment_method, payment_purpose, qr_string, midtrans_trans_id, raw_notification, created_at, updated_at, paid_at
		FROM payments WHERE booking_id=$1`
	var p domain.Payment
	err := r.pool.QueryRow(ctx, q, bookingID).Scan(
		&p.ID, &p.BookingID, &p.OrderID, &p.AmountIDR, &p.Status,
		&p.PaymentMethod, &p.PaymentPurpose, &p.QRString, &p.MidtransTransID, &p.RawNotification,
		&p.CreatedAt, &p.UpdatedAt, &p.PaidAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *PaymentRepo) MarkPaid(ctx context.Context, orderID, midtransTransID, raw string) error {
	now := time.Now()
	_, err := r.pool.Exec(ctx, `
		UPDATE payments SET status='paid', midtrans_trans_id=$2, raw_notification=$3, paid_at=$4, updated_at=now()
		WHERE order_id=$1 AND status <> 'paid'`, orderID, midtransTransID, raw, now)
	return err
}

func (r *PaymentRepo) MarkStatus(ctx context.Context, orderID string, status domain.PaymentStatus, raw string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE payments SET status=$2, raw_notification=$3, updated_at=now() WHERE order_id=$1`, orderID, status, raw)
	return err
}
