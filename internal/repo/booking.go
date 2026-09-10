package repo

import (
	"context"
	"errors"
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
	const q = `INSERT INTO bookings (
		id, customer_id, location_id, service_id, staff_id, start_at, end_at, status, price_idr, hold_expires_at, notes
	) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
	RETURNING created_at, updated_at`
	err := r.pool.QueryRow(ctx, q,
		b.ID, b.CustomerID, b.LocationID, b.ServiceID, b.StaffID,
		b.StartAt, b.EndAt, b.Status, b.PriceIDR, b.HoldExpiresAt, b.Notes,
	).Scan(&b.CreatedAt, &b.UpdatedAt)
	return err
}

func (r *BookingRepo) ByID(ctx context.Context, id uuid.UUID) (*domain.Booking, error) {
	const q = `SELECT b.id, b.customer_id, b.location_id, b.service_id, b.staff_id, b.start_at, b.end_at,
		b.status, b.price_idr, b.hold_expires_at, b.notes, b.created_at, b.updated_at,
		COALESCE(u.full_name,''), COALESCE(sv.name,''), COALESCE(st.name,''), COALESCE(l.name,'')
		FROM bookings b
		LEFT JOIN users u ON u.id = b.customer_id
		LEFT JOIN services sv ON sv.id = b.service_id
		LEFT JOIN staff st ON st.id = b.staff_id
		LEFT JOIN locations l ON l.id = b.location_id
		WHERE b.id=$1`
	var b domain.Booking
	err := r.pool.QueryRow(ctx, q, id).Scan(
		&b.ID, &b.CustomerID, &b.LocationID, &b.ServiceID, &b.StaffID, &b.StartAt, &b.EndAt,
		&b.Status, &b.PriceIDR, &b.HoldExpiresAt, &b.Notes, &b.CreatedAt, &b.UpdatedAt,
		&b.CustomerName, &b.ServiceName, &b.StaffName, &b.LocationName,
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
		b.status, b.price_idr, b.hold_expires_at, b.notes, b.created_at, b.updated_at,
		COALESCE(u.full_name,''), COALESCE(sv.name,''), COALESCE(st.name,''), COALESCE(l.name,'')
		FROM bookings b
		LEFT JOIN users u ON u.id = b.customer_id
		LEFT JOIN services sv ON sv.id = b.service_id
		LEFT JOIN staff st ON st.id = b.staff_id
		LEFT JOIN locations l ON l.id = b.location_id
		WHERE b.customer_id=$1
		ORDER BY b.start_at DESC`
	return r.scanList(ctx, q, customerID)
}

func (r *BookingRepo) ListUpcoming(ctx context.Context, limit int) ([]domain.Booking, error) {
	const q = `SELECT b.id, b.customer_id, b.location_id, b.service_id, b.staff_id, b.start_at, b.end_at,
		b.status, b.price_idr, b.hold_expires_at, b.notes, b.created_at, b.updated_at,
		COALESCE(u.full_name,''), COALESCE(sv.name,''), COALESCE(st.name,''), COALESCE(l.name,'')
		FROM bookings b
		LEFT JOIN users u ON u.id = b.customer_id
		LEFT JOIN services sv ON sv.id = b.service_id
		LEFT JOIN staff st ON st.id = b.staff_id
		LEFT JOIN locations l ON l.id = b.location_id
		WHERE b.start_at >= now() - interval '1 day'
		ORDER BY b.start_at ASC
		LIMIT $1`
	return r.scanList(ctx, q, limit)
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
			&b.Status, &b.PriceIDR, &b.HoldExpiresAt, &b.Notes, &b.CreatedAt, &b.UpdatedAt,
			&b.CustomerName, &b.ServiceName, &b.StaffName, &b.LocationName,
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

func (r *BookingRepo) ExpireHolds(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `
		UPDATE bookings SET status='expired', updated_at=now()
		WHERE status='pending_payment' AND hold_expires_at IS NOT NULL AND hold_expires_at < now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (r *BookingRepo) Reschedule(ctx context.Context, id uuid.UUID, start, end time.Time, staffID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE bookings SET start_at=$2, end_at=$3, staff_id=$4, updated_at=now()
		WHERE id=$1 AND status='confirmed'`, id, start, end, staffID)
	return err
}

func (r *BookingRepo) UpdateDetails(ctx context.Context, id, staffID uuid.UUID, start, end time.Time, status domain.BookingStatus, notes string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE bookings SET staff_id=$2, start_at=$3, end_at=$4, status=$5, notes=$6, updated_at=now()
		WHERE id=$1`, id, staffID, start, end, status, notes)
	return err
}

type PaymentRepo struct{ pool *pgxpool.Pool }

func NewPaymentRepo(pool *pgxpool.Pool) *PaymentRepo { return &PaymentRepo{pool: pool} }

func (r *PaymentRepo) Create(ctx context.Context, p *domain.Payment) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	const q = `INSERT INTO payments (id, booking_id, order_id, amount_idr, status, qr_string, midtrans_trans_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING created_at, updated_at`
	return r.pool.QueryRow(ctx, q, p.ID, p.BookingID, p.OrderID, p.AmountIDR, p.Status, p.QRString, p.MidtransTransID).
		Scan(&p.CreatedAt, &p.UpdatedAt)
}

func (r *PaymentRepo) ByOrderID(ctx context.Context, orderID string) (*domain.Payment, error) {
	const q = `SELECT id, booking_id, order_id, amount_idr, status, qr_string, midtrans_trans_id, raw_notification, created_at, updated_at, paid_at
		FROM payments WHERE order_id=$1`
	var p domain.Payment
	err := r.pool.QueryRow(ctx, q, orderID).Scan(
		&p.ID, &p.BookingID, &p.OrderID, &p.AmountIDR, &p.Status, &p.QRString, &p.MidtransTransID, &p.RawNotification,
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
	const q = `SELECT id, booking_id, order_id, amount_idr, status, qr_string, midtrans_trans_id, raw_notification, created_at, updated_at, paid_at
		FROM payments WHERE booking_id=$1`
	var p domain.Payment
	err := r.pool.QueryRow(ctx, q, bookingID).Scan(
		&p.ID, &p.BookingID, &p.OrderID, &p.AmountIDR, &p.Status, &p.QRString, &p.MidtransTransID, &p.RawNotification,
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
