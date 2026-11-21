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

// TVTokenRepo stores waiting-room kiosk tokens. Tokens are read by value on
// every poll and revoked by setting revoked_at — nothing is ever deleted, so a
// revoked link keeps a creation trail for the admin page.
type TVTokenRepo struct{ pool *pgxpool.Pool }

func NewTVTokenRepo(pool *pgxpool.Pool) *TVTokenRepo { return &TVTokenRepo{pool: pool} }

const tvTokenCols = `id, token, label, location_id, created_at, revoked_at`

func scanTVToken(row pgx.Row) (domain.TVToken, error) {
	var t domain.TVToken
	err := row.Scan(&t.ID, &t.Token, &t.Label, &t.LocationID, &t.CreatedAt, &t.RevokedAt)
	return t, err
}

// Create inserts a token. The caller mints the secret (service.NewAccessToken);
// this method only assigns the id when it is missing.
func (r *TVTokenRepo) Create(ctx context.Context, t *domain.TVToken) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	const q = `INSERT INTO tv_tokens (id, token, label, location_id) VALUES ($1,$2,$3,$4) RETURNING created_at`
	var created time.Time
	if err := r.pool.QueryRow(ctx, q, t.ID, t.Token, t.Label, t.LocationID).Scan(&created); err != nil {
		return err
	}
	t.CreatedAt = &created
	return nil
}

// ActiveByToken returns the live row for a presented token. A revoked or
// unknown token is ErrNotFound — the handler renders the same minimal page
// either way, so an outsider cannot tell the two apart.
func (r *TVTokenRepo) ActiveByToken(ctx context.Context, token string) (*domain.TVToken, error) {
	t, err := scanTVToken(r.pool.QueryRow(ctx,
		`SELECT `+tvTokenCols+` FROM tv_tokens WHERE token=$1 AND revoked_at IS NULL`, token))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// Revoke kills a live token. Revoking twice is ErrNotFound so the admin page
// can report "already gone" instead of silently pretending it worked.
func (r *TVTokenRepo) Revoke(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `UPDATE tv_tokens SET revoked_at=now() WHERE id=$1 AND revoked_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListActive returns live tokens, newest first, for the admin page.
func (r *TVTokenRepo) ListActive(ctx context.Context) ([]domain.TVToken, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+tvTokenCols+` FROM tv_tokens WHERE revoked_at IS NULL ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.TVToken
	for rows.Next() {
		t, err := scanTVToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
