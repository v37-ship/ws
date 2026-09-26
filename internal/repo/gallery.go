package repo

import (
	"context"
	"errors"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type GalleryRepo struct{ pool *pgxpool.Pool }

func NewGalleryRepo(pool *pgxpool.Pool) *GalleryRepo { return &GalleryRepo{pool: pool} }

const galleryCols = `g.id, g.kind, g.title, g.caption, g.service_id, COALESCE(s.name, ''),
	g.image_path, g.image_url, g.video_url, g.thumb_path, g.thumb_url,
	g.sort, g.active, g.created_at, g.updated_at`

const galleryFrom = ` FROM gallery_items g LEFT JOIN services s ON s.id = g.service_id`

func scanGallery(row pgx.Row) (domain.GalleryItem, error) {
	var g domain.GalleryItem
	err := row.Scan(
		&g.ID, &g.Kind, &g.Title, &g.Caption, &g.ServiceID, &g.ServiceName,
		&g.ImagePath, &g.ImageURL, &g.VideoURL, &g.ThumbPath, &g.ThumbURL,
		&g.Sort, &g.Active, &g.CreatedAt, &g.UpdatedAt,
	)
	return g, err
}

// ListActive returns visible gallery rows in display order.
func (r *GalleryRepo) ListActive(ctx context.Context) ([]domain.GalleryItem, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+galleryCols+galleryFrom+` WHERE g.active ORDER BY g.sort, g.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.GalleryItem
	for rows.Next() {
		g, err := scanGallery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ListAll returns every row for the portal manager.
func (r *GalleryRepo) ListAll(ctx context.Context) ([]domain.GalleryItem, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+galleryCols+galleryFrom+` ORDER BY g.sort, g.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.GalleryItem
	for rows.Next() {
		g, err := scanGallery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (r *GalleryRepo) ByID(ctx context.Context, id uuid.UUID) (*domain.GalleryItem, error) {
	g, err := scanGallery(r.pool.QueryRow(ctx, `SELECT `+galleryCols+galleryFrom+` WHERE g.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

func (r *GalleryRepo) Create(ctx context.Context, g *domain.GalleryItem) error {
	const q = `INSERT INTO gallery_items (id, kind, title, caption, service_id, image_path, image_url, video_url, thumb_path, thumb_url, sort, active)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		RETURNING created_at, updated_at`
	if g.ID == uuid.Nil {
		g.ID = uuid.New()
	}
	return r.pool.QueryRow(ctx, q,
		g.ID, g.Kind, g.Title, g.Caption, g.ServiceID, g.ImagePath, g.ImageURL,
		g.VideoURL, g.ThumbPath, g.ThumbURL, g.Sort, g.Active,
	).Scan(&g.CreatedAt, &g.UpdatedAt)
}

func (r *GalleryRepo) Update(ctx context.Context, g *domain.GalleryItem) error {
	const q = `UPDATE gallery_items SET kind=$2, title=$3, caption=$4, service_id=$5, image_path=$6, image_url=$7, video_url=$8, thumb_path=$9, thumb_url=$10, sort=$11, active=$12, updated_at=now()
		WHERE id=$1 RETURNING updated_at`
	return r.pool.QueryRow(ctx, q,
		g.ID, g.Kind, g.Title, g.Caption, g.ServiceID, g.ImagePath, g.ImageURL,
		g.VideoURL, g.ThumbPath, g.ThumbURL, g.Sort, g.Active,
	).Scan(&g.UpdatedAt)
}

func (r *GalleryRepo) ToggleActive(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE gallery_items SET active = NOT active, updated_at=now() WHERE id=$1`, id)
	return err
}

func (r *GalleryRepo) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM gallery_items WHERE id=$1`, id)
	return err
}

func (r *GalleryRepo) SetSort(ctx context.Context, id uuid.UUID, sort int) error {
	_, err := r.pool.Exec(ctx, `UPDATE gallery_items SET sort=$2, updated_at=now() WHERE id=$1`, id, sort)
	return err
}

func (r *GalleryRepo) NextSort(ctx context.Context) (int, error) {
	var n *int
	err := r.pool.QueryRow(ctx, `SELECT max(sort)+1 FROM gallery_items`).Scan(&n)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	if n == nil {
		return 0, nil
	}
	return *n, nil
}
