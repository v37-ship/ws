package repo

import (
	"context"
	"errors"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WaSettingsRepo persists the singleton WhatsApp configuration row and the
// per-template copy overrides. NULL/empty columns mean "use the env default".
type WaSettingsRepo struct{ pool *pgxpool.Pool }

func NewWaSettingsRepo(pool *pgxpool.Pool) *WaSettingsRepo {
	return &WaSettingsRepo{pool: pool}
}

// Get returns the single settings row, or an empty value if migrations have
// not seeded it yet.
func (r *WaSettingsRepo) Get(ctx context.Context) (*domain.WaSettings, error) {
	var s domain.WaSettings
	err := r.pool.QueryRow(ctx, `
		SELECT customer_enabled, reminder_enabled, remind_hour, review_enabled,
		       review_url, group_mode, staff_group_jid, digest_enabled,
		       digest_hour, outbound_enabled, updated_by, updated_at
		FROM wa_settings WHERE id=1`).
		Scan(&s.CustomerEnabled, &s.ReminderEnabled, &s.RemindHour, &s.ReviewEnabled,
			&s.ReviewURL, &s.GroupMode, &s.StaffGroupJID, &s.DigestEnabled,
			&s.DigestHour, &s.OutboundEnabled, &s.UpdatedBy, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return &s, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// Save writes the whole row (every field), so clearing a value back to NULL is
// just saving the empty settings.
func (r *WaSettingsRepo) Save(ctx context.Context, s domain.WaSettings, by uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO wa_settings (id, customer_enabled, reminder_enabled, remind_hour,
			review_enabled, review_url, group_mode, staff_group_jid, digest_enabled,
			digest_hour, outbound_enabled, updated_by, updated_at)
		VALUES (1, $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, now())
		ON CONFLICT (id) DO UPDATE SET
			customer_enabled = EXCLUDED.customer_enabled,
			reminder_enabled = EXCLUDED.reminder_enabled,
			remind_hour      = EXCLUDED.remind_hour,
			review_enabled   = EXCLUDED.review_enabled,
			review_url       = EXCLUDED.review_url,
			group_mode       = EXCLUDED.group_mode,
			staff_group_jid  = EXCLUDED.staff_group_jid,
			digest_enabled   = EXCLUDED.digest_enabled,
			digest_hour      = EXCLUDED.digest_hour,
			outbound_enabled = EXCLUDED.outbound_enabled,
			updated_by       = EXCLUDED.updated_by,
			updated_at       = now()`,
		s.CustomerEnabled, s.ReminderEnabled, s.RemindHour, s.ReviewEnabled,
		s.ReviewURL, s.GroupMode, s.StaffGroupJID, s.DigestEnabled,
		s.DigestHour, s.OutboundEnabled, by)
	return err
}

// ListOverrides returns the admin copy overrides keyed by template.
func (r *WaSettingsRepo) ListOverrides(ctx context.Context) (map[string]string, error) {
	rows, err := r.pool.Query(ctx, `SELECT template_key, body FROM wa_template_overrides`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, body string
		if err := rows.Scan(&k, &body); err != nil {
			return nil, err
		}
		out[k] = body
	}
	return out, rows.Err()
}

func (r *WaSettingsRepo) SetOverride(ctx context.Context, key, body string, by uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO wa_template_overrides (template_key, body, updated_by, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (template_key) DO UPDATE SET
			body = EXCLUDED.body, updated_by = EXCLUDED.updated_by, updated_at = now()`,
		key, body, by)
	return err
}

func (r *WaSettingsRepo) DeleteOverride(ctx context.Context, key string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM wa_template_overrides WHERE template_key=$1`, key)
	return err
}
