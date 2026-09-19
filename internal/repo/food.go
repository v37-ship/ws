package repo

import (
	"context"
	"errors"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type FoodRepo struct{ pool *pgxpool.Pool }

func NewFoodRepo(pool *pgxpool.Pool) *FoodRepo { return &FoodRepo{pool: pool} }

const foodCols = `id, category, name, description, price_m, price_l, variants, active, image_path, image_url, sort, created_at, updated_at`

func scanFood(row pgx.Row) (domain.FoodItem, error) {
	var f domain.FoodItem
	err := row.Scan(
		&f.ID, &f.Category, &f.Name, &f.Description, &f.PriceM, &f.PriceL,
		&f.Variants, &f.Active, &f.ImagePath, &f.ImageURL,
		&f.Sort, &f.CreatedAt, &f.UpdatedAt,
	)
	return f, err
}

const foodOrder = ` ORDER BY CASE category WHEN 'food' THEN 0 WHEN 'beverage' THEN 1 ELSE 2 END, sort, name`

// ListActive returns visible menu rows, food section first.
func (r *FoodRepo) ListActive(ctx context.Context) ([]domain.FoodItem, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+foodCols+` FROM food_items WHERE active`+foodOrder)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.FoodItem
	for rows.Next() {
		f, err := scanFood(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ListAll returns every row for the portal manager.
func (r *FoodRepo) ListAll(ctx context.Context) ([]domain.FoodItem, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+foodCols+` FROM food_items`+foodOrder)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.FoodItem
	for rows.Next() {
		f, err := scanFood(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (r *FoodRepo) ByID(ctx context.Context, id uuid.UUID) (*domain.FoodItem, error) {
	f, err := scanFood(r.pool.QueryRow(ctx, `SELECT `+foodCols+` FROM food_items WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (r *FoodRepo) Create(ctx context.Context, f *domain.FoodItem) error {
	const q = `INSERT INTO food_items (id, category, name, description, price_m, price_l, variants, active, image_path, image_url, sort)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
		RETURNING created_at, updated_at`
	if f.ID == uuid.Nil {
		f.ID = uuid.New()
	}
	return r.pool.QueryRow(ctx, q,
		f.ID, f.Category, f.Name, f.Description, f.PriceM, f.PriceL,
		f.Variants, f.Active, f.ImagePath, f.ImageURL, f.Sort,
	).Scan(&f.CreatedAt, &f.UpdatedAt)
}

func (r *FoodRepo) Update(ctx context.Context, f *domain.FoodItem) error {
	const q = `UPDATE food_items SET category=$2, name=$3, description=$4, price_m=$5, price_l=$6, variants=$7, active=$8, image_path=$9, image_url=$10, sort=$11, updated_at=now()
		WHERE id=$1 RETURNING updated_at`
	return r.pool.QueryRow(ctx, q,
		f.ID, f.Category, f.Name, f.Description, f.PriceM, f.PriceL,
		f.Variants, f.Active, f.ImagePath, f.ImageURL, f.Sort,
	).Scan(&f.UpdatedAt)
}

func (r *FoodRepo) ToggleActive(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE food_items SET active = NOT active, updated_at=now() WHERE id=$1`, id)
	return err
}

func (r *FoodRepo) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM food_items WHERE id=$1`, id)
	return err
}

func (r *FoodRepo) Count(ctx context.Context) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM food_items`).Scan(&n)
	return n, err
}
