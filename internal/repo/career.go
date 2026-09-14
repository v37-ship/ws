package repo

import (
	"context"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CareerRepo struct{ pool *pgxpool.Pool }

func NewCareerRepo(pool *pgxpool.Pool) *CareerRepo { return &CareerRepo{pool: pool} }

func (r *CareerRepo) ListJobs(ctx context.Context, activeOnly bool) ([]domain.Job, error) {
	q := `SELECT j.id, j.title, j.location_id, COALESCE(l.name,''),
		j.employment_type, j.description, j.requirements, j.active, j.created_at, j.updated_at
		FROM jobs j LEFT JOIN locations l ON l.id = j.location_id`
	if activeOnly {
		q += ` WHERE j.active=true`
	}
	q += ` ORDER BY j.created_at DESC`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Job
	for rows.Next() {
		var j domain.Job
		if err := rows.Scan(&j.ID, &j.Title, &j.LocationID, &j.LocationName,
			&j.EmploymentType, &j.Description, &j.Requirements, &j.Active, &j.CreatedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (r *CareerRepo) JobByID(ctx context.Context, id uuid.UUID) (*domain.Job, error) {
	const q = `SELECT j.id, j.title, j.location_id, COALESCE(l.name,''),
		j.employment_type, j.description, j.requirements, j.active, j.created_at, j.updated_at
		FROM jobs j LEFT JOIN locations l ON l.id = j.location_id WHERE j.id=$1`
	var j domain.Job
	if err := r.pool.QueryRow(ctx, q, id).Scan(&j.ID, &j.Title, &j.LocationID, &j.LocationName,
		&j.EmploymentType, &j.Description, &j.Requirements, &j.Active, &j.CreatedAt, &j.UpdatedAt); err != nil {
		return nil, err
	}
	return &j, nil
}

func (r *CareerRepo) SaveJob(ctx context.Context, j *domain.Job) error {
	if j.ID == uuid.Nil {
		j.ID = uuid.New()
	}
	const q = `INSERT INTO jobs (id, title, location_id, employment_type, description, requirements, active)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`
	_, err := r.pool.Exec(ctx, q, j.ID, j.Title, j.LocationID, j.EmploymentType, j.Description, j.Requirements, j.Active)
	return err
}

func (r *CareerRepo) ToggleJobActive(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE jobs SET active = NOT active, updated_at=now() WHERE id=$1`, id)
	return err
}

func (r *CareerRepo) SaveApplication(ctx context.Context, a *domain.JobApplication) error {
	if a.ID == uuid.Nil {
		a.ID = uuid.New()
	}
	const q = `INSERT INTO job_applications (id, job_id, full_name, email, phone, message, cv_filename, cv_original_name)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`
	_, err := r.pool.Exec(ctx, q, a.ID, a.JobID, a.FullName, a.Email, a.Phone, a.Message, a.CVFilename, a.CVOriginalName)
	return err
}

func (r *CareerRepo) ListApplications(ctx context.Context, jobID uuid.UUID) ([]domain.JobApplication, error) {
	const q = `SELECT a.id, a.job_id, j.title, a.full_name, a.email, a.phone, a.message,
		a.cv_filename, a.cv_original_name, a.created_at
		FROM job_applications a JOIN jobs j ON j.id = a.job_id
		WHERE a.job_id=$1 ORDER BY a.created_at DESC`
	rows, err := r.pool.Query(ctx, q, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.JobApplication
	for rows.Next() {
		var a domain.JobApplication
		if err := rows.Scan(&a.ID, &a.JobID, &a.JobTitle, &a.FullName, &a.Email, &a.Phone,
			&a.Message, &a.CVFilename, &a.CVOriginalName, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *CareerRepo) ApplicationByID(ctx context.Context, id uuid.UUID) (*domain.JobApplication, error) {
	const q = `SELECT a.id, a.job_id, j.title, a.full_name, a.email, a.phone, a.message,
		a.cv_filename, a.cv_original_name, a.created_at
		FROM job_applications a JOIN jobs j ON j.id = a.job_id WHERE a.id=$1`
	var a domain.JobApplication
	if err := r.pool.QueryRow(ctx, q, id).Scan(&a.ID, &a.JobID, &a.JobTitle, &a.FullName, &a.Email, &a.Phone,
		&a.Message, &a.CVFilename, &a.CVOriginalName, &a.CreatedAt); err != nil {
		return nil, err
	}
	return &a, nil
}
