package repo

import (
	"context"
	"errors"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PromoRepo struct{ pool *pgxpool.Pool }

func NewPromoRepo(pool *pgxpool.Pool) *PromoRepo { return &PromoRepo{pool: pool} }

const promoCols = `id, code, kind, value, min_spend, max_uses, used_count, active, valid_from, valid_to, created_at, updated_at`

func scanPromo(row pgx.Row) (domain.PromoCode, error) {
	var p domain.PromoCode
	err := row.Scan(
		&p.ID, &p.Code, &p.Kind, &p.Value, &p.MinSpend, &p.MaxUses,
		&p.UsedCount, &p.Active, &p.ValidFrom, &p.ValidTo,
		&p.CreatedAt, &p.UpdatedAt,
	)
	return p, err
}

func (r *PromoRepo) Create(ctx context.Context, p *domain.PromoCode) error {
	const q = `INSERT INTO promo_codes (id, code, kind, value, min_spend, max_uses, active, valid_from, valid_to)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING created_at, updated_at`
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	return r.pool.QueryRow(ctx, q,
		p.ID, p.Code, p.Kind, p.Value, p.MinSpend, p.MaxUses,
		p.Active, p.ValidFrom, p.ValidTo,
	).Scan(&p.CreatedAt, &p.UpdatedAt)
}

// ByCode finds a code case-insensitively (codes are stored uppercase).
func (r *PromoRepo) ByCode(ctx context.Context, code string) (*domain.PromoCode, error) {
	p, err := scanPromo(r.pool.QueryRow(ctx, `SELECT `+promoCols+` FROM promo_codes WHERE code=$1`, code))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *PromoRepo) ListAll(ctx context.Context) ([]domain.PromoCode, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+promoCols+` FROM promo_codes ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.PromoCode
	for rows.Next() {
		p, err := scanPromo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *PromoRepo) ToggleActive(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE promo_codes SET active = NOT active, updated_at=now() WHERE id=$1`, id)
	return err
}

func (r *PromoRepo) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM promo_codes WHERE id=$1`, id)
	return err
}

// TryRedeem atomically consumes one use, succeeding only while the code is
// active and under its cap. Callers re-validate everything else first.
func (r *PromoRepo) TryRedeem(ctx context.Context, code string) error {
	tag, err := r.pool.Exec(ctx, `UPDATE promo_codes
		SET used_count = used_count + 1, updated_at=now()
		WHERE code=$1 AND active AND (max_uses IS NULL OR used_count < max_uses)`, code)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Unredeem releases a use consumed by a booking that then failed to charge.
func (r *PromoRepo) Unredeem(ctx context.Context, code string) error {
	_, err := r.pool.Exec(ctx, `UPDATE promo_codes
		SET used_count = GREATEST(used_count - 1, 0), updated_at=now()
		WHERE code=$1`, code)
	return err
}
