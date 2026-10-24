package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WaOutboxItem is one due row claimed by the WhatsApp worker.
type WaOutboxItem struct {
	ID          uuid.UUID
	Phone       string
	TemplateKey string
	Payload     []byte
	BookingID   *uuid.UUID
	DedupeKey   *string
	ScheduledAt time.Time
	Attempts    int
	MaxAttempts int
	LastError   string
	Status      string
	CreatedAt   time.Time
}

type WaRepo struct{ pool *pgxpool.Pool }

func NewWaRepo(pool *pgxpool.Pool) *WaRepo { return &WaRepo{pool: pool} }

// Enqueue adds a pending message. A non-empty dedupe key makes the call
// idempotent: the second insert is ignored and inserted=false is returned.
// scheduledAt zero means "now".
func (r *WaRepo) Enqueue(ctx context.Context, phone, templateKey string, payload map[string]any, bookingID *uuid.UUID, dedupeKey string, scheduledAt time.Time) (bool, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("encode wa payload: %w", err)
	}
	if scheduledAt.IsZero() {
		scheduledAt = time.Now()
	}
	var dedupe *string
	if dedupeKey != "" {
		dedupe = &dedupeKey
	}
	const q = `INSERT INTO wa_outbox (phone, template_key, payload, booking_id, dedupe_key, scheduled_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (dedupe_key) DO NOTHING
		RETURNING id`
	var id uuid.UUID
	err = r.pool.QueryRow(ctx, q, phone, templateKey, raw, bookingID, dedupe, scheduledAt).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// UpsertScheduled inserts a message for later (or moves one that has not been
// delivered yet). Enqueue cannot do this: dedupe_key is unique forever, so a
// second insert is a silent no-op. Here an existing pending or failed row is
// moved to the new time and retried, while sent/cancelled rows are left alone
// (so history is never resurrected).
func (r *WaRepo) UpsertScheduled(ctx context.Context, phone, templateKey string, payload map[string]any, bookingID *uuid.UUID, dedupeKey string, scheduledAt time.Time) (bool, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("encode wa payload: %w", err)
	}
	if scheduledAt.IsZero() {
		scheduledAt = time.Now()
	}
	if dedupeKey == "" {
		return r.Enqueue(ctx, phone, templateKey, payload, bookingID, "", scheduledAt)
	}
	const q = `INSERT INTO wa_outbox (phone, template_key, payload, booking_id, dedupe_key, scheduled_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (dedupe_key) DO UPDATE
			SET scheduled_at = EXCLUDED.scheduled_at, phone = EXCLUDED.phone,
			    payload = EXCLUDED.payload, status = 'pending', attempts = 0,
			    last_error = '', updated_at = now()
			WHERE wa_outbox.status IN ('pending', 'failed')
		RETURNING id`
	var id uuid.UUID
	err = r.pool.QueryRow(ctx, q, phone, templateKey, raw, bookingID, dedupeKey, scheduledAt).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// CancelPending drops scheduled rows that have not gone out yet: every pending
// row for the booking, or only the given dedupe keys. Used when a booking is
// cancelled/rescheduled/expired so a stale reminder cannot fire later.
func (r *WaRepo) CancelPending(ctx context.Context, bookingID uuid.UUID, keys ...string) (int64, error) {
	if len(keys) == 0 {
		tag, err := r.pool.Exec(ctx,
			`UPDATE wa_outbox SET status='cancelled', updated_at=now()
			 WHERE booking_id=$1 AND status='pending'`, bookingID)
		if err != nil {
			return 0, err
		}
		return tag.RowsAffected(), nil
	}
	tag, err := r.pool.Exec(ctx,
		`UPDATE wa_outbox SET status='cancelled', updated_at=now()
		 WHERE booking_id=$1 AND status='pending' AND dedupe_key = ANY($2)`, bookingID, keys)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ClaimDue atomically claims up to limit due rows (FOR UPDATE SKIP LOCKED, so
// several workers would not double-send). The attempt counter is incremented
// on claim, so the returned Attempts is the current try (1 for the first).
func (r *WaRepo) ClaimDue(ctx context.Context, limit int) ([]WaOutboxItem, error) {
	const q = `
		WITH due AS (
			SELECT id FROM wa_outbox
			WHERE status = 'pending' AND scheduled_at <= now()
			ORDER BY scheduled_at
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		)
		UPDATE wa_outbox o
		SET status = 'sending', attempts = o.attempts + 1, updated_at = now()
		FROM due
		WHERE o.id = due.id
		RETURNING o.id, o.phone, o.template_key, o.payload, o.booking_id, o.dedupe_key,
		          o.scheduled_at, o.attempts, o.max_attempts, o.last_error`
	rows, err := r.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WaOutboxItem
	for rows.Next() {
		var it WaOutboxItem
		if err := rows.Scan(&it.ID, &it.Phone, &it.TemplateKey, &it.Payload, &it.BookingID,
			&it.DedupeKey, &it.ScheduledAt, &it.Attempts, &it.MaxAttempts, &it.LastError); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (r *WaRepo) MarkSent(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE wa_outbox SET status='sent', last_error='', updated_at=now() WHERE id=$1`, id)
	return err
}

func (r *WaRepo) RetryLater(ctx context.Context, id uuid.UUID, nextAt time.Time, errMsg string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE wa_outbox SET status='pending', scheduled_at=$2, last_error=$3, updated_at=now() WHERE id=$1`,
		id, nextAt, errMsg)
	return err
}

func (r *WaRepo) MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error {
	_, err := r.pool.Exec(ctx, `UPDATE wa_outbox SET status='failed', last_error=$2, updated_at=now() WHERE id=$1`, id, errMsg)
	return err
}

// RequeueSending resets rows left in 'sending' by a crash/restart.
func (r *WaRepo) RequeueSending(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `UPDATE wa_outbox SET status='pending', updated_at=now() WHERE status='sending'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ListRecent returns the most recently created outbox rows (admin log).
func (r *WaRepo) ListRecent(ctx context.Context, limit int) ([]WaOutboxItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	const q = `SELECT id, phone, template_key, payload, booking_id, dedupe_key,
		scheduled_at, attempts, max_attempts, last_error, status, created_at
		FROM wa_outbox ORDER BY created_at DESC LIMIT $1`
	return r.scanList(ctx, q, limit)
}

// ListUpcoming returns pending rows scheduled for the future, soonest first.
// templateKey filters to one template (empty = every template).
func (r *WaRepo) ListUpcoming(ctx context.Context, templateKey string, limit int) ([]WaOutboxItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	const q = `SELECT id, phone, template_key, payload, booking_id, dedupe_key,
		scheduled_at, attempts, max_attempts, last_error, status, created_at
		FROM wa_outbox
		WHERE status='pending' AND scheduled_at > now()
		  AND ($1 = '' OR template_key = $1)
		ORDER BY scheduled_at ASC LIMIT $2`
	rows, err := r.pool.Query(ctx, q, templateKey, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WaOutboxItem
	for rows.Next() {
		var it WaOutboxItem
		if err := rows.Scan(&it.ID, &it.Phone, &it.TemplateKey, &it.Payload, &it.BookingID,
			&it.DedupeKey, &it.ScheduledAt, &it.Attempts, &it.MaxAttempts, &it.LastError,
			&it.Status, &it.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// scanList reads outbox rows in the shared column order.
func (r *WaRepo) scanList(ctx context.Context, q string, arg any) ([]WaOutboxItem, error) {
	rows, err := r.pool.Query(ctx, q, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WaOutboxItem
	for rows.Next() {
		var it WaOutboxItem
		if err := rows.Scan(&it.ID, &it.Phone, &it.TemplateKey, &it.Payload, &it.BookingID,
			&it.DedupeKey, &it.ScheduledAt, &it.Attempts, &it.MaxAttempts, &it.LastError,
			&it.Status, &it.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ListByBooking returns the latest outbox rows for a booking (portal history).
func (r *WaRepo) ListByBooking(ctx context.Context, bookingID uuid.UUID, limit int) ([]WaOutboxItem, error) {
	if limit <= 0 || limit > 50 {
		limit = 5
	}
	const q = `SELECT id, phone, template_key, payload, booking_id, dedupe_key,
		scheduled_at, attempts, max_attempts, last_error, status, created_at
		FROM wa_outbox WHERE booking_id=$1 ORDER BY created_at DESC LIMIT $2`
	rows, err := r.pool.Query(ctx, q, bookingID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WaOutboxItem
	for rows.Next() {
		var it WaOutboxItem
		if err := rows.Scan(&it.ID, &it.Phone, &it.TemplateKey, &it.Payload, &it.BookingID,
			&it.DedupeKey, &it.ScheduledAt, &it.Attempts, &it.MaxAttempts, &it.LastError,
			&it.Status, &it.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}
