package repo

import (
	"context"
	"errors"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PaymentSettingsRepo struct{ pool *pgxpool.Pool }

func NewPaymentSettingsRepo(pool *pgxpool.Pool) *PaymentSettingsRepo {
	return &PaymentSettingsRepo{pool: pool}
}

// Get returns the single settings row, or an empty value if migrations have
// not seeded it yet.
func (r *PaymentSettingsRepo) Get(ctx context.Context) (*domain.PaymentSettings, error) {
	var s domain.PaymentSettings
	err := r.pool.QueryRow(ctx,
		`SELECT qris_image_path, updated_by, updated_at FROM payment_settings WHERE id=1`).
		Scan(&s.QRISImagePath, &s.UpdatedBy, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return &s, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *PaymentSettingsRepo) SetQRISImage(ctx context.Context, path string, by uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO payment_settings (id, qris_image_path, updated_by, updated_at)
		VALUES (1, $1, $2, now())
		ON CONFLICT (id) DO UPDATE SET qris_image_path=$1, updated_by=$2, updated_at=now()`, path, by)
	return err
}
