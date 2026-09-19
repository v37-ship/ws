package repo

import (
	"context"
	"errors"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type TestimonialRepo struct{ pool *pgxpool.Pool }

func NewTestimonialRepo(pool *pgxpool.Pool) *TestimonialRepo {
	return &TestimonialRepo{pool: pool}
}

const testimonialCols = `id, customer_id, name, age, photo_path, photo_url, rating, message, status, created_at, updated_at`

func scanTestimonial(row pgx.Row) (domain.Testimonial, error) {
	var t domain.Testimonial
	err := row.Scan(
		&t.ID, &t.CustomerID, &t.Name, &t.Age, &t.PhotoPath, &t.PhotoURL,
		&t.Rating, &t.Message, &t.Status, &t.CreatedAt, &t.UpdatedAt,
	)
	return t, err
}

func (r *TestimonialRepo) Create(ctx context.Context, t *domain.Testimonial) error {
	const q = `INSERT INTO testimonials (id, customer_id, name, age, photo_path, photo_url, rating, message, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING created_at, updated_at`
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	if t.Status == "" {
		t.Status = domain.TestimonialPending
	}
	return r.pool.QueryRow(ctx, q,
		t.ID, t.CustomerID, t.Name, t.Age, t.PhotoPath, t.PhotoURL,
		t.Rating, t.Message, t.Status,
	).Scan(&t.CreatedAt, &t.UpdatedAt)
}

// ListApproved returns published testimonials, newest first.
func (r *TestimonialRepo) ListApproved(ctx context.Context, limit int) ([]domain.Testimonial, error) {
	if limit <= 0 || limit > 50 {
		limit = 12
	}
	rows, err := r.pool.Query(ctx, `SELECT `+testimonialCols+` FROM testimonials
		WHERE status='approved' ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Testimonial
	for rows.Next() {
		t, err := scanTestimonial(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListAll returns every testimonial for the admin manager, newest first.
func (r *TestimonialRepo) ListAll(ctx context.Context) ([]domain.Testimonial, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+testimonialCols+` FROM testimonials ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Testimonial
	for rows.Next() {
		t, err := scanTestimonial(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *TestimonialRepo) ByID(ctx context.Context, id uuid.UUID) (*domain.Testimonial, error) {
	t, err := scanTestimonial(r.pool.QueryRow(ctx, `SELECT `+testimonialCols+` FROM testimonials WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *TestimonialRepo) SetStatus(ctx context.Context, id uuid.UUID, status domain.TestimonialStatus) error {
	_, err := r.pool.Exec(ctx, `UPDATE testimonials SET status=$2, updated_at=now() WHERE id=$1`, id, status)
	return err
}

func (r *TestimonialRepo) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM testimonials WHERE id=$1`, id)
	return err
}

func (r *TestimonialRepo) Count(ctx context.Context) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM testimonials`).Scan(&n)
	return n, err
}

// TouchCreatedAt backdates a row so seeds read newest-first in roster order.
func (r *TestimonialRepo) TouchCreatedAt(ctx context.Context, id uuid.UUID, daysAgo int) error {
	_, err := r.pool.Exec(ctx, `UPDATE testimonials SET created_at = now() - make_interval(days => $2), updated_at = now() WHERE id=$1`, id, daysAgo)
	return err
}
