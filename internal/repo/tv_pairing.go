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

// TVPairingRepo stores the ephemeral pairing handshake: one row per screen
// showing a code. Rows are claimed exactly once (Consume sets consumed_at) and
// swept by the app ticker, so the unauthenticated-write surface stays bounded
// (ruling #10).
type TVPairingRepo struct{ pool *pgxpool.Pool }

func NewTVPairingRepo(pool *pgxpool.Pool) *TVPairingRepo { return &TVPairingRepo{pool: pool} }

const tvPairingCols = `id, pending_key, code, location_id, device_id, expires_at, consumed_at, created_at`

func scanTVPairing(row pgx.Row) (domain.TVPairing, error) {
	var p domain.TVPairing
	var consumed *time.Time
	err := row.Scan(&p.ID, &p.PendingKey, &p.Code, &p.LocationID, &p.DeviceID,
		&p.ExpiresAt, &consumed, &p.CreatedAt)
	if consumed != nil {
		p.ConsumedAt = *consumed
	}
	return p, err
}

// tvPairingFromRow maps a no-row result onto ErrNotFound. Every consumer of a
// pairing (the poll, the portal's consume) treats "no such live row" as one
// case; a driver error stays a driver error.
func tvPairingFromRow(row pgx.Row) (*domain.TVPairing, error) {
	p, err := scanTVPairing(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// Create inserts a pending pairing. The caller mints the pending key
// (service.NewAccessToken) and the six-digit code; this method only assigns
// the id and returns the database's created_at.
func (r *TVPairingRepo) Create(ctx context.Context, p *domain.TVPairing) error {
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	const q = `INSERT INTO tv_pairings (id, pending_key, code, expires_at) VALUES ($1,$2,$3,$4) RETURNING created_at`
	return r.pool.QueryRow(ctx, q, p.ID, p.PendingKey, p.Code, p.ExpiresAt).Scan(&p.CreatedAt)
}

// ByPendingKey returns the row the TV's pending cookie points at. An unknown
// key (expired and swept, or a forged cookie) is ErrNotFound, which the poll
// renders as "code expired" — the two are deliberately indistinguishable.
func (r *TVPairingRepo) ByPendingKey(ctx context.Context, key string) (*domain.TVPairing, error) {
	return tvPairingFromRow(r.pool.QueryRow(ctx,
		`SELECT `+tvPairingCols+` FROM tv_pairings WHERE pending_key=$1`, key))
}

// tvPairingConsumeSQL claims a code for a branch.
//
// This has to be ONE statement: the guards it writes against (unconsumed,
// unexpired) are re-checked in the same predicate that sets consumed_at, so
// two staff members entering the same code in the same second produce exactly
// one winner and the loser sees ErrNotFound. The oldest live row wins if two
// screens ever show the same six digits — the branch comes from the CS either
// way, so nothing leaks in that astronomically rare case.
const tvPairingConsumeSQL = `UPDATE tv_pairings SET consumed_at=now(), location_id=$2
WHERE code=$1 AND consumed_at IS NULL AND expires_at>now()
RETURNING ` + tvPairingCols

// Consume atomically claims a live code and binds it to the branch the CS
// chose. A wrong, used, or expired code is ErrNotFound (the portal renders one
// honest message for all three, so the form is not a code oracle).
func (r *TVPairingRepo) Consume(ctx context.Context, code string, loc uuid.UUID) (*domain.TVPairing, error) {
	return tvPairingFromRow(r.pool.QueryRow(ctx, tvPairingConsumeSQL, code, loc))
}

// LinkDevice closes the handshake (ruling #6): the newly minted device is
// attached to its pairing, which is what the TV's poll reads to know it has
// been paired.
func (r *TVPairingRepo) LinkDevice(ctx context.Context, pairingID, deviceID uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE tv_pairings SET device_id=$2 WHERE id=$1`, pairingID, deviceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Sweep deletes handshake rows that can never be claimed again: expired rows
// at once, consumed rows after a short grace. The grace is what lets the TV's
// 4s poll still read its pairing right after the counter links the device —
// deleting a consumed row instantly would race that final poll (a lost poll
// would orphan a perfectly good device). Ten minutes is still a hard bound on
// the unauthenticated-write surface.
func (r *TVPairingRepo) Sweep(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM tv_pairings
WHERE expires_at < now() OR consumed_at < now() - interval '10 minutes'`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// TVDeviceRepo stores paired waiting-room screens. A device outlives its
// pairing row by design: after LinkDevice the hash is the only trace of the
// secret, and the row is never deleted (revocation is a timestamp, mirroring
// TVTokenRepo's creation trail).
type TVDeviceRepo struct{ pool *pgxpool.Pool }

func NewTVDeviceRepo(pool *pgxpool.Pool) *TVDeviceRepo { return &TVDeviceRepo{pool: pool} }

const tvDeviceCols = `id, secret_hash, label, location_id, paired_by, paired_at, last_seen, revoked_at`

func scanTVDevice(row pgx.Row) (domain.TVDevice, error) {
	var d domain.TVDevice
	err := row.Scan(&d.ID, &d.SecretHash, &d.Label, &d.LocationID, &d.PairedBy,
		&d.PairedAt, &d.LastSeen, &d.RevokedAt)
	return d, err
}

// Create inserts a freshly paired device and returns the database's paired_at.
// The caller has already hashed the secret (ruling #9).
func (r *TVDeviceRepo) Create(ctx context.Context, d *domain.TVDevice) error {
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	const q = `INSERT INTO tv_devices (id, secret_hash, label, location_id, paired_by)
VALUES ($1,$2,$3,$4,$5) RETURNING paired_at`
	var at time.Time
	if err := r.pool.QueryRow(ctx, q, d.ID, d.SecretHash, d.Label, d.LocationID, d.PairedBy).Scan(&at); err != nil {
		return err
	}
	d.PairedAt = &at
	return nil
}

// ByID returns the device row for a presented cookie id. Revocation is not
// filtered here: the chain checks Active() so a revoked device is reported,
// not confused with a forgery.
func (r *TVDeviceRepo) ByID(ctx context.Context, id uuid.UUID) (*domain.TVDevice, error) {
	d, err := scanTVDevice(r.pool.QueryRow(ctx,
		`SELECT `+tvDeviceCols+` FROM tv_devices WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// ListActive returns live devices, most recently paired first.
func (r *TVDeviceRepo) ListActive(ctx context.Context) ([]domain.TVDevice, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+tvDeviceCols+` FROM tv_devices WHERE revoked_at IS NULL ORDER BY paired_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.TVDevice
	for rows.Next() {
		d, err := scanTVDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// TouchLastSeen records that the screen is alive, throttled so the 30s poll
// does not rewrite the row every half minute: the update only lands when the
// stamp is older than threshold (or never set).
func (r *TVDeviceRepo) TouchLastSeen(ctx context.Context, id uuid.UUID, threshold time.Duration) error {
	cutoff := time.Now().Add(-threshold)
	_, err := r.pool.Exec(ctx,
		`UPDATE tv_devices SET last_seen=now()
WHERE id=$1 AND (last_seen IS NULL OR last_seen < $2)`, id, cutoff)
	return err
}

// Revoke kills a live device. Revoking twice is ErrNotFound, so the admin page
// can report "already gone" instead of pretending it worked (TVTokenRepo
// precedent).
func (r *TVDeviceRepo) Revoke(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE tv_devices SET revoked_at=now() WHERE id=$1 AND revoked_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
