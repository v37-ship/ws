package repo

import (
	"context"
	"errors"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type HeroRepo struct{ pool *pgxpool.Pool }

func NewHeroRepo(pool *pgxpool.Pool) *HeroRepo { return &HeroRepo{pool: pool} }

const heroCols = `id, image_path, image_url, sort, active, created_at, updated_at`

func scanHero(row pgx.Row) (domain.HeroBanner, error) {
	var h domain.HeroBanner
	err := row.Scan(
		&h.ID, &h.ImagePath, &h.ImageURL, &h.Sort, &h.Active,
		&h.CreatedAt, &h.UpdatedAt,
	)
	return h, err
}

// ListActive returns visible banners in display order.
func (r *HeroRepo) ListActive(ctx context.Context) ([]domain.HeroBanner, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+heroCols+` FROM hero_banners WHERE active ORDER BY sort, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.HeroBanner
	for rows.Next() {
		h, err := scanHero(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ListAll returns every banner for the portal manager.
func (r *HeroRepo) ListAll(ctx context.Context) ([]domain.HeroBanner, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+heroCols+` FROM hero_banners ORDER BY sort, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.HeroBanner
	for rows.Next() {
		h, err := scanHero(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (r *HeroRepo) Create(ctx context.Context, h *domain.HeroBanner) error {
	const q = `INSERT INTO hero_banners (id, image_path, image_url, sort, active)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING created_at, updated_at`
	if h.ID == uuid.Nil {
		h.ID = uuid.New()
	}
	return r.pool.QueryRow(ctx, q,
		h.ID, h.ImagePath, h.ImageURL, h.Sort, h.Active,
	).Scan(&h.CreatedAt, &h.UpdatedAt)
}

func (r *HeroRepo) SetSort(ctx context.Context, id uuid.UUID, sort int) error {
	_, err := r.pool.Exec(ctx, `UPDATE hero_banners SET sort=$2, updated_at=now() WHERE id=$1`, id, sort)
	return err
}

func (r *HeroRepo) ToggleActive(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE hero_banners SET active = NOT active, updated_at=now() WHERE id=$1`, id)
	return err
}

func (r *HeroRepo) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM hero_banners WHERE id=$1`, id)
	return err
}

func (r *HeroRepo) NextSort(ctx context.Context) (int, error) {
	var n *int
	err := r.pool.QueryRow(ctx, `SELECT max(sort)+1 FROM hero_banners`).Scan(&n)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	if n == nil {
		return 0, nil
	}
	return *n, nil
}
