package repo

import (
	"context"
	"errors"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DistanceRepo struct{ pool *pgxpool.Pool }

func NewDistanceRepo(pool *pgxpool.Pool) *DistanceRepo { return &DistanceRepo{pool: pool} }

const distanceCols = `id, min_km, max_km, fee_idr, active, sort, created_at, updated_at`

func scanDistance(row pgx.Row) (domain.DistanceZone, error) {
	var z domain.DistanceZone
	err := row.Scan(
		&z.ID, &z.MinKM, &z.MaxKM, &z.FeeIDR,
		&z.Active, &z.Sort, &z.CreatedAt, &z.UpdatedAt,
	)
	return z, err
}

func (r *DistanceRepo) Create(ctx context.Context, z *domain.DistanceZone) error {
	if z.ID == uuid.Nil {
		z.ID = uuid.New()
	}
	const q = `INSERT INTO distance_zones (id, min_km, max_km, fee_idr, active, sort)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING created_at, updated_at`
	return r.pool.QueryRow(ctx, q,
		z.ID, z.MinKM, z.MaxKM, z.FeeIDR, z.Active, z.Sort,
	).Scan(&z.CreatedAt, &z.UpdatedAt)
}

func (r *DistanceRepo) Update(ctx context.Context, z *domain.DistanceZone) error {
	tag, err := r.pool.Exec(ctx, `UPDATE distance_zones
		SET min_km=$2, max_km=$3, fee_idr=$4, active=$5, sort=$6, updated_at=now()
		WHERE id=$1`, z.ID, z.MinKM, z.MaxKM, z.FeeIDR, z.Active, z.Sort)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *DistanceRepo) ByID(ctx context.Context, id uuid.UUID) (*domain.DistanceZone, error) {
	z, err := scanDistance(r.pool.QueryRow(ctx, `SELECT `+distanceCols+` FROM distance_zones WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &z, nil
}

func (r *DistanceRepo) ListAll(ctx context.Context) ([]domain.DistanceZone, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+distanceCols+` FROM distance_zones ORDER BY min_km, sort`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectZones(rows)
}

func (r *DistanceRepo) ListActive(ctx context.Context) ([]domain.DistanceZone, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+distanceCols+` FROM distance_zones WHERE active ORDER BY min_km, sort`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectZones(rows)
}

func collectZones(rows pgx.Rows) ([]domain.DistanceZone, error) {
	var out []domain.DistanceZone
	for rows.Next() {
		z, err := scanDistance(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, z)
	}
	return out, rows.Err()
}

func (r *DistanceRepo) Count(ctx context.Context) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM distance_zones`).Scan(&n)
	return n, err
}

func (r *DistanceRepo) ToggleActive(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE distance_zones SET active = NOT active, updated_at=now() WHERE id=$1`, id)
	return err
}

func (r *DistanceRepo) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM distance_zones WHERE id=$1`, id)
	return err
}

// Overlaps reports whether another active band already covers [minKM, maxKM].
// Bands may touch at the edges (0–1 and 1–2) but must not cross.
func (r *DistanceRepo) Overlaps(ctx context.Context, id uuid.UUID, minKM, maxKM float64) (bool, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM distance_zones
		WHERE active AND id <> $1
		  AND min_km < $3 AND max_km > $2`, id, minKM, maxKM).Scan(&n)
	return n > 0, err
}
