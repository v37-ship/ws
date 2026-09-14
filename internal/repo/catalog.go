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

type CatalogRepo struct{ pool *pgxpool.Pool }

func NewCatalogRepo(pool *pgxpool.Pool) *CatalogRepo { return &CatalogRepo{pool: pool} }

func (r *CatalogRepo) ListLocations(ctx context.Context, activeOnly bool) ([]domain.Location, error) {
	q := `SELECT id, name, address, city, timezone, active, created_at, updated_at FROM locations`
	if activeOnly {
		q += ` WHERE active=true`
	}
	q += ` ORDER BY name`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Location
	for rows.Next() {
		var l domain.Location
		if err := rows.Scan(&l.ID, &l.Name, &l.Address, &l.City, &l.Timezone, &l.Active, &l.CreatedAt, &l.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (r *CatalogRepo) LocationByID(ctx context.Context, id uuid.UUID) (*domain.Location, error) {
	const q = `SELECT id, name, address, city, timezone, active, created_at, updated_at FROM locations WHERE id=$1`
	var l domain.Location
	err := r.pool.QueryRow(ctx, q, id).Scan(&l.ID, &l.Name, &l.Address, &l.City, &l.Timezone, &l.Active, &l.CreatedAt, &l.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &l, nil
}

func (r *CatalogRepo) CreateLocation(ctx context.Context, l *domain.Location) error {
	if l.ID == uuid.Nil {
		l.ID = uuid.New()
	}
	if l.Timezone == "" {
		l.Timezone = "Asia/Jakarta"
	}
	const q = `INSERT INTO locations (id, name, address, city, timezone, active)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING created_at, updated_at`
	return r.pool.QueryRow(ctx, q, l.ID, l.Name, l.Address, l.City, l.Timezone, l.Active).
		Scan(&l.CreatedAt, &l.UpdatedAt)
}

func (r *CatalogRepo) UpdateLocation(ctx context.Context, l *domain.Location) error {
	const q = `UPDATE locations SET name=$2, address=$3, city=$4, timezone=$5, active=$6, updated_at=now()
		WHERE id=$1 RETURNING updated_at`
	return r.pool.QueryRow(ctx, q, l.ID, l.Name, l.Address, l.City, l.Timezone, l.Active).Scan(&l.UpdatedAt)
}

func (r *CatalogRepo) ListServices(ctx context.Context, locationID uuid.UUID, activeOnly bool) ([]domain.Service, error) {
	q := `SELECT id, location_id, name, description, duration_min, price_idr, active, image_path, image_url, created_at, updated_at
		FROM services WHERE location_id=$1`
	if activeOnly {
		q += ` AND active=true`
	}
	q += ` ORDER BY name`
	rows, err := r.pool.Query(ctx, q, locationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Service
	for rows.Next() {
		var s domain.Service
		if err := rows.Scan(&s.ID, &s.LocationID, &s.Name, &s.Description, &s.DurationMin, &s.PriceIDR, &s.Active, &s.ImagePath, &s.ImageURL, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *CatalogRepo) ServiceByID(ctx context.Context, id uuid.UUID) (*domain.Service, error) {
	const q = `SELECT id, location_id, name, description, duration_min, price_idr, active, image_path, image_url, created_at, updated_at
		FROM services WHERE id=$1`
	var s domain.Service
	err := r.pool.QueryRow(ctx, q, id).Scan(&s.ID, &s.LocationID, &s.Name, &s.Description, &s.DurationMin, &s.PriceIDR, &s.Active, &s.ImagePath, &s.ImageURL, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *CatalogRepo) CreateService(ctx context.Context, s *domain.Service) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	const q = `INSERT INTO services (id, location_id, name, description, duration_min, price_idr, active, image_path, image_url)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING created_at, updated_at`
	return r.pool.QueryRow(ctx, q, s.ID, s.LocationID, s.Name, s.Description, s.DurationMin, s.PriceIDR, s.Active, s.ImagePath, s.ImageURL).
		Scan(&s.CreatedAt, &s.UpdatedAt)
}

func (r *CatalogRepo) UpdateService(ctx context.Context, s *domain.Service) error {
	const q = `UPDATE services SET name=$2, description=$3, duration_min=$4, price_idr=$5, active=$6, image_path=$7, image_url=$8, updated_at=now()
		WHERE id=$1 RETURNING updated_at`
	return r.pool.QueryRow(ctx, q, s.ID, s.Name, s.Description, s.DurationMin, s.PriceIDR, s.Active, s.ImagePath, s.ImageURL).Scan(&s.UpdatedAt)
}

func (r *CatalogRepo) ListStaff(ctx context.Context, locationID uuid.UUID, activeOnly bool) ([]domain.Staff, error) {
	q := `SELECT id, location_id, name, bio, active, photo_path, photo_url, created_at, updated_at FROM staff WHERE location_id=$1`
	if activeOnly {
		q += ` AND active=true`
	}
	q += ` ORDER BY name`
	rows, err := r.pool.Query(ctx, q, locationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Staff
	for rows.Next() {
		var s domain.Staff
		if err := rows.Scan(&s.ID, &s.LocationID, &s.Name, &s.Bio, &s.Active, &s.PhotoPath, &s.PhotoURL, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *CatalogRepo) StaffByID(ctx context.Context, id uuid.UUID) (*domain.Staff, error) {
	const q = `SELECT id, location_id, name, bio, active, photo_path, photo_url, created_at, updated_at FROM staff WHERE id=$1`
	var s domain.Staff
	err := r.pool.QueryRow(ctx, q, id).Scan(&s.ID, &s.LocationID, &s.Name, &s.Bio, &s.Active, &s.PhotoPath, &s.PhotoURL, &s.CreatedAt, &s.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *CatalogRepo) CreateStaff(ctx context.Context, s *domain.Staff) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	const q = `INSERT INTO staff (id, location_id, name, bio, active, photo_path, photo_url) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING created_at, updated_at`
	return r.pool.QueryRow(ctx, q, s.ID, s.LocationID, s.Name, s.Bio, s.Active, s.PhotoPath, s.PhotoURL).Scan(&s.CreatedAt, &s.UpdatedAt)
}

func (r *CatalogRepo) UpdateStaff(ctx context.Context, s *domain.Staff) error {
	const q = `UPDATE staff SET name=$2, bio=$3, active=$4, photo_path=$5, photo_url=$6, updated_at=now()
		WHERE id=$1 RETURNING updated_at`
	return r.pool.QueryRow(ctx, q, s.ID, s.Name, s.Bio, s.Active, s.PhotoPath, s.PhotoURL).Scan(&s.UpdatedAt)
}

func (r *CatalogRepo) SetStaffServices(ctx context.Context, staffID uuid.UUID, serviceIDs []uuid.UUID) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM staff_services WHERE staff_id=$1`, staffID); err != nil {
		return err
	}
	for _, sid := range serviceIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO staff_services (staff_id, service_id) VALUES ($1,$2)`, staffID, sid); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *CatalogRepo) StaffForService(ctx context.Context, locationID, serviceID uuid.UUID) ([]domain.Staff, error) {
	const q = `SELECT s.id, s.location_id, s.name, s.bio, s.active, s.photo_path, s.photo_url, s.created_at, s.updated_at
		FROM staff s
		JOIN staff_services ss ON ss.staff_id = s.id
		WHERE s.location_id=$1 AND ss.service_id=$2 AND s.active=true
		ORDER BY s.name`
	rows, err := r.pool.Query(ctx, q, locationID, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Staff
	for rows.Next() {
		var s domain.Staff
		if err := rows.Scan(&s.ID, &s.LocationID, &s.Name, &s.Bio, &s.Active, &s.PhotoPath, &s.PhotoURL, &s.CreatedAt, &s.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *CatalogRepo) ListStaffHours(ctx context.Context, staffID uuid.UUID) ([]domain.StaffHours, error) {
	const q = `SELECT id, staff_id, weekday, to_char(start_time,'HH24:MI'), to_char(end_time,'HH24:MI')
		FROM staff_hours WHERE staff_id=$1 ORDER BY weekday, start_time`
	rows, err := r.pool.Query(ctx, q, staffID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.StaffHours
	for rows.Next() {
		var h domain.StaffHours
		if err := rows.Scan(&h.ID, &h.StaffID, &h.Weekday, &h.StartTime, &h.EndTime); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (r *CatalogRepo) ReplaceStaffHours(ctx context.Context, staffID uuid.UUID, hours []domain.StaffHours) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM staff_hours WHERE staff_id=$1`, staffID); err != nil {
		return err
	}
	for _, h := range hours {
		id := h.ID
		if id == uuid.Nil {
			id = uuid.New()
		}
		if _, err := tx.Exec(ctx, `INSERT INTO staff_hours (id, staff_id, weekday, start_time, end_time)
			VALUES ($1,$2,$3,$4::time,$5::time)`, id, staffID, h.Weekday, h.StartTime, h.EndTime); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *CatalogRepo) ListServiceHours(ctx context.Context, serviceID uuid.UUID) ([]domain.ServiceHours, error) {
	const q = `SELECT id, service_id, weekday, to_char(start_time,'HH24:MI'), to_char(end_time,'HH24:MI')
		FROM service_hours WHERE service_id=$1 ORDER BY weekday, start_time`
	rows, err := r.pool.Query(ctx, q, serviceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ServiceHours
	for rows.Next() {
		var h domain.ServiceHours
		if err := rows.Scan(&h.ID, &h.ServiceID, &h.Weekday, &h.StartTime, &h.EndTime); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (r *CatalogRepo) ReplaceServiceHours(ctx context.Context, serviceID uuid.UUID, hours []domain.ServiceHours) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM service_hours WHERE service_id=$1`, serviceID); err != nil {
		return err
	}
	for _, h := range hours {
		id := h.ID
		if id == uuid.Nil {
			id = uuid.New()
		}
		if _, err := tx.Exec(ctx, `INSERT INTO service_hours (id, service_id, weekday, start_time, end_time)
			VALUES ($1,$2,$3,$4::time,$5::time)`, id, serviceID, h.Weekday, h.StartTime, h.EndTime); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (r *CatalogRepo) StaffServiceIDs(ctx context.Context, staffID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.pool.Query(ctx, `SELECT service_id FROM staff_services WHERE staff_id=$1`, staffID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r *CatalogRepo) CountSeed(ctx context.Context) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM locations`).Scan(&n)
	return n, err
}

// BusyIntervals returns active booking windows and approved time-offs for staff on a calendar day (UTC bounds passed in).
func (r *CatalogRepo) BusyIntervals(ctx context.Context, staffID uuid.UUID, from, to time.Time) ([][2]time.Time, error) {
	booked, off, err := r.BusyDetails(ctx, staffID, from, to)
	if err != nil {
		return nil, err
	}
	return append(booked, off...), nil
}

// BusyDetails splits busy time into customer bookings vs staff time off,
// so the UI can label unavailable blocks honestly.
func (r *CatalogRepo) BusyDetails(ctx context.Context, staffID uuid.UUID, from, to time.Time) (booked, off [][2]time.Time, err error) {
	booked, err = r.intervals(ctx, `SELECT start_at, end_at FROM bookings
		WHERE staff_id=$1 AND start_at < $3 AND end_at > $2
		AND (
			status IN ('confirmed', 'checked_in', 'in_service')
			OR (status = 'pending_payment' AND (hold_expires_at IS NULL OR hold_expires_at > now()))
		)`, staffID, from, to)
	if err != nil {
		return nil, nil, err
	}
	off, err = r.intervals(ctx, `SELECT start_at, end_at FROM staff_time_off
		WHERE staff_id=$1 AND start_at < $3 AND end_at > $2`, staffID, from, to)
	if err != nil {
		return nil, nil, err
	}
	return booked, off, nil
}

func (r *CatalogRepo) intervals(ctx context.Context, q string, staffID uuid.UUID, from, to time.Time) ([][2]time.Time, error) {
	rows, err := r.pool.Query(ctx, q, staffID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]time.Time
	for rows.Next() {
		var a, b time.Time
		if err := rows.Scan(&a, &b); err != nil {
			return nil, err
		}
		out = append(out, [2]time.Time{a, b})
	}
	return out, rows.Err()
}

func (r *CatalogRepo) ListStaffTimeOff(ctx context.Context, staffID uuid.UUID) ([]domain.StaffTimeOff, error) {
	var rows pgx.Rows
	var err error
	if staffID != uuid.Nil {
		const q = `SELECT t.id, t.staff_id, s.name, t.start_at, t.end_at, t.reason, t.created_at
			FROM staff_time_off t
			JOIN staff s ON s.id = t.staff_id
			WHERE t.staff_id = $1 AND t.end_at >= now() - interval '1 day'
			ORDER BY t.start_at ASC`
		rows, err = r.pool.Query(ctx, q, staffID)
	} else {
		const q = `SELECT t.id, t.staff_id, s.name, t.start_at, t.end_at, t.reason, t.created_at
			FROM staff_time_off t
			JOIN staff s ON s.id = t.staff_id
			WHERE t.end_at >= now() - interval '1 day'
			ORDER BY t.start_at ASC`
		rows, err = r.pool.Query(ctx, q)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.StaffTimeOff
	for rows.Next() {
		var to domain.StaffTimeOff
		if err := rows.Scan(&to.ID, &to.StaffID, &to.StaffName, &to.StartAt, &to.EndAt, &to.Reason, &to.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, to)
	}
	return out, rows.Err()
}

func (r *CatalogRepo) CreateStaffTimeOff(ctx context.Context, to *domain.StaffTimeOff) error {
	if to.ID == uuid.Nil {
		to.ID = uuid.New()
	}
	const q = `INSERT INTO staff_time_off (id, staff_id, start_at, end_at, reason) VALUES ($1,$2,$3,$4,$5) RETURNING created_at`
	return r.pool.QueryRow(ctx, q, to.ID, to.StaffID, to.StartAt, to.EndAt, to.Reason).Scan(&to.CreatedAt)
}

func (r *CatalogRepo) DeleteStaffTimeOff(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM staff_time_off WHERE id=$1`, id)
	return err
}

func (r *CatalogRepo) ToggleServiceActive(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE services SET active = NOT active, updated_at=now() WHERE id=$1`, id)
	return err
}

func (r *CatalogRepo) ToggleStaffActive(ctx context.Context, id uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `UPDATE staff SET active = NOT active, updated_at=now() WHERE id=$1`, id)
	return err
}

// StaffBookingCount reports how many bookings reference a therapist.
// Bookings use ON DELETE RESTRICT, so history must be absent for removal.
func (r *CatalogRepo) StaffBookingCount(ctx context.Context, staffID uuid.UUID) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM bookings WHERE staff_id=$1`, staffID).Scan(&n)
	return n, err
}

// DeleteStaff removes a therapist; hours, service mapping, and time-off
// rows cascade. Fails on RESTRICT when booking history exists.
func (r *CatalogRepo) DeleteStaff(ctx context.Context, staffID uuid.UUID) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM staff WHERE id=$1`, staffID)
	return err
}
