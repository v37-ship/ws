package repo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type UserRepo struct{ pool *pgxpool.Pool }

func NewUserRepo(pool *pgxpool.Pool) *UserRepo { return &UserRepo{pool: pool} }

func (r *UserRepo) Create(ctx context.Context, u *domain.User) error {
	const q = `INSERT INTO users (id, email, password_hash, full_name, phone, role)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING created_at, updated_at`
	if u.ID == uuid.Nil {
		u.ID = uuid.New()
	}
	return r.pool.QueryRow(ctx, q, u.ID, u.Email, u.PasswordHash, u.FullName, u.Phone, u.Role).
		Scan(&u.CreatedAt, &u.UpdatedAt)
}

func (r *UserRepo) ByEmail(ctx context.Context, email string) (*domain.User, error) {
	const q = `SELECT id, email, password_hash, full_name, phone, role, created_at, updated_at
		FROM users WHERE email=$1`
	var u domain.User
	err := r.pool.QueryRow(ctx, q, email).Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.FullName, &u.Phone, &u.Role, &u.CreatedAt, &u.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *UserRepo) ByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	const q = `SELECT id, email, password_hash, full_name, phone, role, created_at, updated_at
		FROM users WHERE id=$1`
	var u domain.User
	err := r.pool.QueryRow(ctx, q, id).Scan(
		&u.ID, &u.Email, &u.PasswordHash, &u.FullName, &u.Phone, &u.Role, &u.CreatedAt, &u.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *UserRepo) EnsureAdmin(ctx context.Context, email, passwordHash, fullName string) error {
	return r.ensureRole(ctx, email, passwordHash, fullName, domain.RoleAdmin)
}

func (r *UserRepo) EnsureCS(ctx context.Context, email, passwordHash, fullName string) error {
	return r.ensureRole(ctx, email, passwordHash, fullName, domain.RoleCS)
}

func (r *UserRepo) ensureRole(ctx context.Context, email, passwordHash, fullName string, role domain.Role) error {
	_, err := r.ByEmail(ctx, email)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	u := &domain.User{
		Email:        email,
		PasswordHash: passwordHash,
		FullName:     fullName,
		Role:         role,
	}
	return r.Create(ctx, u)
}

func (r *UserRepo) ListByRole(ctx context.Context, role domain.Role) ([]domain.User, error) {
	const q = `SELECT id, email, password_hash, full_name, phone, role, created_at, updated_at
		FROM users WHERE role=$1 ORDER BY full_name`
	rows, err := r.pool.Query(ctx, q, role)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.User
	for rows.Next() {
		var u domain.User
		if err := rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.FullName, &u.Phone, &u.Role, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *UserRepo) ListCustomers(ctx context.Context, q string) ([]domain.User, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return r.ListByRole(ctx, domain.RoleCustomer)
	}
	like := "%" + strings.ToLower(q) + "%"
	const sql = `SELECT id, email, password_hash, full_name, phone, role, created_at, updated_at
		FROM users
		WHERE role='customer' AND (lower(email) LIKE $1 OR lower(full_name) LIKE $1 OR phone LIKE $1)
		ORDER BY full_name`
	rows, err := r.pool.Query(ctx, sql, like)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.User
	for rows.Next() {
		var u domain.User
		if err := rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.FullName, &u.Phone, &u.Role, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (r *UserRepo) Update(ctx context.Context, u *domain.User) error {
	const q = `UPDATE users SET email=$2, full_name=$3, phone=$4, role=$5, updated_at=now()
		WHERE id=$1 RETURNING updated_at`
	return r.pool.QueryRow(ctx, q, u.ID, u.Email, u.FullName, u.Phone, u.Role).Scan(&u.UpdatedAt)
}

func (r *UserRepo) UpdatePassword(ctx context.Context, id uuid.UUID, passwordHash string) error {
	_, err := r.pool.Exec(ctx, `UPDATE users SET password_hash=$2, updated_at=now() WHERE id=$1`, id, passwordHash)
	return err
}

type SessionRepo struct{ pool *pgxpool.Pool }

func NewSessionRepo(pool *pgxpool.Pool) *SessionRepo { return &SessionRepo{pool: pool} }

func (r *SessionRepo) Create(ctx context.Context, s *domain.Session) error {
	const q = `INSERT INTO sessions (id, user_id, expires_at) VALUES ($1,$2,$3) RETURNING created_at`
	return r.pool.QueryRow(ctx, q, s.ID, s.UserID, s.ExpiresAt).Scan(&s.CreatedAt)
}

func (r *SessionRepo) ByID(ctx context.Context, id string) (*domain.Session, error) {
	const q = `SELECT id, user_id, expires_at, created_at FROM sessions WHERE id=$1`
	var s domain.Session
	err := r.pool.QueryRow(ctx, q, id).Scan(&s.ID, &s.UserID, &s.ExpiresAt, &s.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *SessionRepo) Delete(ctx context.Context, id string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE id=$1`, id)
	return err
}

func (r *SessionRepo) DeleteExpired(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	return err
}

func (r *SessionRepo) Touch(ctx context.Context, id string, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx, `UPDATE sessions SET expires_at=$2 WHERE id=$1`, id, expiresAt)
	return err
}

func fmtErr(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", op, err)
}
