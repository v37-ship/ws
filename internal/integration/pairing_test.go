//go:build integration

package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"
	"spa-booking/internal/service"
)

// The TV pairing handshake is SQL-only behaviour: Consume has to claim a six
// digit code exactly once even when two staff members submit it in the same
// second, and the 1-minute ticker runs two sweeps whose grace windows are the
// only thing keeping a live screen (or a just-paired one) alive. The unit tests
// pin the handlers around these calls; this group executes the statements
// themselves against real rows, where a lost predicate or a shifted interval
// actually shows up.

// pairingFixture is two branches (the CS picks one when claiming a code, so a
// claim that ignores the caller's choice is caught) plus the real pairing and
// device repos over the shared test pool.
type pairingFixture struct {
	pool     *pgxpool.Pool
	pairings *repo.TVPairingRepo
	devices  *repo.TVDeviceRepo
	loc      uuid.UUID
	other    uuid.UUID
}

// newPairingFixture truncates the schema and seeds the two branches the
// pairing/device foreign keys require.
func newPairingFixture(t *testing.T) *pairingFixture {
	t.Helper()
	pool := OpenTestDB(t)
	TruncateAll(t, pool)
	return &pairingFixture{
		pool:     pool,
		pairings: repo.NewTVPairingRepo(pool),
		devices:  repo.NewTVDeviceRepo(pool),
		loc:      insertLocation(t, pool, "Pairing Branch"),
		other:    insertLocation(t, pool, "Pairing Branch Two"),
	}
}

// newCode creates one pending pairing through the production repo, the same
// way the kiosk does: a minted pending key, an explicit six-digit code and an
// explicit expiry.
func (fx *pairingFixture) newCode(t *testing.T, code string, ttl time.Duration) *domain.TVPairing {
	t.Helper()
	key, err := service.NewAccessToken()
	if err != nil {
		t.Fatalf("mint pending key: %v", err)
	}
	p := &domain.TVPairing{PendingKey: key, Code: code, ExpiresAt: time.Now().Add(ttl)}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := fx.pairings.Create(ctx, p); err != nil {
		t.Fatalf("create pairing %s: %v", code, err)
	}
	if p.CreatedAt.IsZero() {
		t.Fatal("create pairing did not return created_at")
	}
	return p
}

// pinExpiresAt backdates a pairing's expiry. Sweep reads only expires_at, so
// this is how an unclaimed-but-lapsed row is produced without waiting.
func (fx *pairingFixture) pinExpiresAt(t *testing.T, id uuid.UUID, at time.Time) {
	t.Helper()
	fx.exec(t, `UPDATE tv_pairings SET expires_at=$2 WHERE id=$1`, id, at)
}

// pinConsumedAt backdates a claim. Consume always stamps now(); the sweep's
// ten-minute grace is only observable by moving the stamp, so the older half of
// a consumed pair is written directly (nothing under test reads created_at).
func (fx *pairingFixture) pinConsumedAt(t *testing.T, id uuid.UUID, at time.Time) {
	t.Helper()
	fx.exec(t, `UPDATE tv_pairings SET consumed_at=$2 WHERE id=$1`, id, at)
}

// exec runs one fixture mutation with its own timeout.
func (fx *pairingFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := fx.pool.Exec(ctx, query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

// pairingState is the mutable slice of a pairing row the assertions read back.
type pairingState struct {
	consumedAt *time.Time
	locationID *uuid.UUID
}

// state reads a pairing row directly. Sweeps delete rows, so "gone" is a state
// to assert on rather than an error.
func (fx *pairingFixture) state(t *testing.T, id uuid.UUID) (pairingState, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var st pairingState
	err := fx.pool.QueryRow(ctx,
		`SELECT consumed_at, location_id FROM tv_pairings WHERE id=$1`, id).
		Scan(&st.consumedAt, &st.locationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return pairingState{}, false
	}
	if err != nil {
		t.Fatalf("read pairing %s: %v", id, err)
	}
	return st, true
}

// countRows is the scalar read every membership assertion goes through.
func (fx *pairingFixture) countRows(t *testing.T, query string, args ...any) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var n int
	if err := fx.pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

// deviceSeed is the writable subset insertTVDevice accepts. Required:
// LocationID. Zero PairedAt means now; SecretHash defaults to the hash of a
// fresh secret, which is the only shape the column ever holds in production.
type deviceSeed struct {
	LocationID uuid.UUID
	Label      string
	SecretHash string
	PairedAt   *time.Time
	LastSeen   *time.Time
	RevokedAt  *time.Time
}

// insertTVDevice writes one paired-screen row with explicit timestamps, so a
// sweep test can age a device by a day (or a year) without waiting. Direct
// inserts bypass repo.Create's now() stamp on purpose: paired_at is the only
// column SweepPhantoms grades and it is not writable through the repo.
func insertTVDevice(t *testing.T, pool *pgxpool.Pool, seed deviceSeed) uuid.UUID {
	t.Helper()
	if seed.LocationID == uuid.Nil {
		t.Fatal("insertTVDevice: LocationID is required")
	}
	if seed.SecretHash == "" {
		seed.SecretHash = domain.TVSecretHash(uuid.NewString())
	}
	pairedAt := time.Now()
	if seed.PairedAt != nil {
		pairedAt = *seed.PairedAt
	}
	id := uuid.New()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx,
		`INSERT INTO tv_devices (id, secret_hash, label, location_id, paired_at, last_seen, revoked_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		id, seed.SecretHash, seed.Label, seed.LocationID, pairedAt, seed.LastSeen, seed.RevokedAt,
	); err != nil {
		t.Fatalf("insertTVDevice: %v", err)
	}
	return id
}

// consumeClaim is one goroutine's outcome in the claim race.
type consumeClaim struct {
	branch  uuid.UUID
	pairing *domain.TVPairing
	err     error
}

// raceConsume starts two claims on one code behind a shared start barrier — on
// separate pool connections, so the two UPDATEs meet on the row at the same
// moment — and returns the one winner and the one ErrNotFound. Anything else is
// a failure: a driver error must never masquerade as a lost race.
func raceConsume(t *testing.T, fx *pairingFixture, code string) (winner, loser consumeClaim) {
	t.Helper()
	branches := []uuid.UUID{fx.loc, fx.other}
	claims := make([]consumeClaim, len(branches))

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range claims {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			pairing, err := fx.pairings.Consume(ctx, code, branches[i])
			claims[i] = consumeClaim{branch: branches[i], pairing: pairing, err: err}
		}(i)
	}
	close(start)
	wg.Wait()

	var winners, losers []consumeClaim
	for _, c := range claims {
		switch {
		case c.err == nil:
			winners = append(winners, c)
		case errors.Is(c.err, repo.ErrNotFound):
			losers = append(losers, c)
		default:
			t.Fatalf("claim %s at branch %s failed unexpectedly: %v", code, c.branch, c.err)
		}
	}
	if len(winners) != 1 || len(losers) != 1 {
		t.Fatalf("race %s produced %d winners / %d losers, want exactly one winner and one ErrNotFound",
			code, len(winners), len(losers))
	}
	return winners[0], losers[0]
}

// TestTVPairingConsumeRace is the one-winner rule: the claim is a single
// UPDATE whose WHERE re-checks the guard, so two simultaneous claims produce
// exactly one pairing and one ErrNotFound — never two winners and never a claim
// that silently rewrites the branch the other one bound.
func TestTVPairingConsumeRace(t *testing.T) {
	fx := newPairingFixture(t)

	// Fresh code per round: a single round could pass by luck if the two
	// statements happened to serialise, while an unguarded read-then-write
	// implementation fails one of five races with near certainty.
	for _, code := range []string{"482915", "317204", "905178", "624803", "158360"} {
		p := fx.newCode(t, code, 5*time.Minute)
		win, lose := raceConsume(t, fx, p.Code)

		if win.branch == lose.branch {
			t.Fatalf("%s: both claims came from branch %s", code, win.branch)
		}
		if win.pairing.ID != p.ID {
			t.Errorf("%s: winner claimed pairing %s, want the seeded %s", code, win.pairing.ID, p.ID)
		}
		if win.pairing.Code != p.Code {
			t.Errorf("%s: winner code = %q, want %q", code, win.pairing.Code, p.Code)
		}
		if win.pairing.ConsumedAt.IsZero() {
			t.Errorf("%s: winner returned without consumed_at — the claim did not write the column it read", code)
		}
		if win.pairing.Live() {
			t.Errorf("%s: winner's pairing still reports Live() after being consumed", code)
		}
		// The branch is the caller's, not whichever row the statement happened
		// to look at: the loser asking for the other branch must not move it.
		if win.pairing.LocationID == nil || *win.pairing.LocationID != win.branch {
			t.Errorf("%s: winner bound to branch %v, want the branch it claimed for (%s)",
				code, win.pairing.LocationID, win.branch)
		}

		st, ok := fx.state(t, p.ID)
		if !ok {
			t.Fatalf("%s: the claimed pairing is gone — Consume must not delete", code)
		}
		if st.consumedAt == nil {
			t.Errorf("%s: stored row has no consumed_at after a successful claim", code)
		}
		if st.locationID == nil || *st.locationID != win.branch {
			t.Errorf("%s: stored branch = %v, want the winner's %s", code, st.locationID, win.branch)
		}
		// One row per code, one of them consumed: the loser matched zero rows
		// instead of restamping a second.
		if got := fx.countRows(t, `SELECT count(*) FROM tv_pairings WHERE code=$1`, p.Code); got != 1 {
			t.Errorf("%s: rows for the code = %d, want 1", code, got)
		}
		if got := fx.countRows(t, `SELECT count(*) FROM tv_pairings WHERE code=$1 AND consumed_at IS NOT NULL`, p.Code); got != 1 {
			t.Errorf("%s: consumed rows for the code = %d, want exactly 1", code, got)
		}
	}
}

// TestTVPairingConsumeMisses pins the three ways a claim must answer
// ErrNotFound — wrong code, lapsed code, already-claimed code — and that a miss
// writes nothing: the live row stays claimable, and a claimed row keeps its
// original stamp and branch.
func TestTVPairingConsumeMisses(t *testing.T) {
	fx := newPairingFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Wrong code: no row matches, and the real live row is untouched.
	live := fx.newCode(t, "100001", 5*time.Minute)
	if _, err := fx.pairings.Consume(ctx, "100002", fx.loc); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("wrong code = %v, want ErrNotFound", err)
	}
	st, ok := fx.state(t, live.ID)
	if !ok {
		t.Fatal("a wrong code deleted the live pairing")
	}
	if st.consumedAt != nil || st.locationID != nil {
		t.Errorf("wrong code wrote to the live row: consumed=%v branch=%v", st.consumedAt, st.locationID)
	}
	// The miss did not burn the code: the next claim with the real value wins
	// and binds the branch asked for then.
	claimed, err := fx.pairings.Consume(ctx, live.Code, fx.other)
	if err != nil {
		t.Fatalf("claim after a wrong attempt: %v", err)
	}
	if claimed.LocationID == nil || *claimed.LocationID != fx.other {
		t.Errorf("claim after a wrong attempt bound %v, want %s", claimed.LocationID, fx.other)
	}

	// Lapsed code: the expiry guard is part of the same predicate. The row is
	// not consumed and not deleted (the sweep, not Consume, removes it).
	expired := fx.newCode(t, "100003", 5*time.Minute)
	fx.pinExpiresAt(t, expired.ID, time.Now().Add(-time.Minute))
	if _, err := fx.pairings.Consume(ctx, expired.Code, fx.loc); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("expired code = %v, want ErrNotFound", err)
	}
	st, ok = fx.state(t, expired.ID)
	if !ok {
		t.Fatal("Consume deleted the expired row; the sweep owns deletion")
	}
	if st.consumedAt != nil {
		t.Error("expired code was stamped consumed by a failed claim")
	}

	// Already claimed: a second claim is ErrNotFound, and the original stamp
	// and branch survive verbatim — no silent re-claim into another branch.
	used := fx.newCode(t, "100004", 5*time.Minute)
	first, err := fx.pairings.Consume(ctx, used.Code, fx.loc)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if first.ConsumedAt.IsZero() {
		t.Fatal("first claim returned no consumed_at")
	}
	if _, err := fx.pairings.Consume(ctx, used.Code, fx.other); !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("second claim of the same code = %v, want ErrNotFound", err)
	}
	st, ok = fx.state(t, used.ID)
	if !ok {
		t.Fatal("a duplicate claim deleted the pairing")
	}
	if st.consumedAt == nil || !st.consumedAt.Equal(first.ConsumedAt) {
		t.Errorf("duplicate claim restamped consumed_at: row=%v first=%v", st.consumedAt, first.ConsumedAt)
	}
	if st.locationID == nil || *st.locationID != fx.loc {
		t.Errorf("duplicate claim moved the branch to %v, want the first claim's %s", st.locationID, fx.loc)
	}
}

// TestTVPairingSweepGraces executes both halves of the ticker's pairing sweep:
// an expired code goes immediately, a consumed row survives its ten-minute
// grace (the TV's final poll reads it) and goes after it.
func TestTVPairingSweepGraces(t *testing.T) {
	fx := newPairingFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	now := time.Now()
	expired := fx.newCode(t, "111111", 5*time.Minute)
	fx.pinExpiresAt(t, expired.ID, now.Add(-time.Minute))

	live := fx.newCode(t, "222222", 5*time.Minute)

	// In-grace claim: produced by the real Consume, so consumed_at is the same
	// now() the ticker's interval is measured against a moment later.
	inGrace := fx.newCode(t, "333333", 5*time.Minute)
	if _, err := fx.pairings.Consume(ctx, inGrace.Code, fx.loc); err != nil {
		t.Fatalf("claim in-grace pairing: %v", err)
	}

	// Past-grace claim. The stamp is moved, not the expiry: this row is still
	// unexpired, so only the consumed branch of the sweep can delete it.
	pastGrace := fx.newCode(t, "444444", 5*time.Minute)
	fx.pinConsumedAt(t, pastGrace.ID, now.Add(-11*time.Minute))

	// Expired AND long consumed: both predicates match, still one delete.
	both := fx.newCode(t, "555555", 5*time.Minute)
	fx.pinExpiresAt(t, both.ID, now.Add(-time.Hour))
	fx.pinConsumedAt(t, both.ID, now.Add(-30*time.Minute))

	// A live device must be outside this sweep's scope entirely.
	device := insertTVDevice(t, fx.pool, deviceSeed{LocationID: fx.loc, Label: "Sweep survivor"})

	deleted, err := fx.pairings.Sweep(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if deleted != 3 {
		t.Errorf("sweep deleted %d rows, want 3 (expired, past-grace consumed, both)", deleted)
	}

	if _, ok := fx.state(t, expired.ID); ok {
		t.Error("expired pairing survived the sweep")
	}
	if _, ok := fx.state(t, both.ID); ok {
		t.Error("expired-and-consumed pairing survived the sweep")
	}
	if _, ok := fx.state(t, pastGrace.ID); ok {
		t.Error("pairing consumed 11 minutes ago survived the sweep — the grace is 10 minutes")
	}
	if _, ok := fx.state(t, inGrace.ID); !ok {
		t.Error("pairing consumed a moment ago was swept inside its 10-minute grace")
	}
	if _, ok := fx.state(t, live.ID); !ok {
		t.Error("live, unclaimed pairing was swept before it expired")
	}

	// The grace is a delay, not a reprieve: once the stamp ages past it the
	// same row is collectable.
	fx.pinConsumedAt(t, inGrace.ID, now.Add(-15*time.Minute))
	if deleted, err = fx.pairings.Sweep(ctx); err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if deleted != 1 {
		t.Errorf("second sweep deleted %d rows, want just the aged claim", deleted)
	}
	if _, ok := fx.state(t, inGrace.ID); ok {
		t.Error("claim aged past the grace survived the second sweep")
	}
	// Nothing to collect is a clean zero, not an error.
	if deleted, err = fx.pairings.Sweep(ctx); err != nil {
		t.Fatalf("third sweep: %v", err)
	}
	if deleted != 0 {
		t.Errorf("idle sweep deleted %d rows, want 0", deleted)
	}

	if got := fx.countRows(t, `SELECT count(*) FROM tv_devices WHERE id=$1`, device); got != 1 {
		t.Error("the pairing sweep deleted a paired device")
	}
}

// TestTVDevicePhantomSweep executes the orphan rule: a device whose TV never
// completed the handshake (last_seen still NULL) is deleted after a day, while
// a screen inside that grace — the one actively pairing right now — and any
// screen that ever polled survive.
func TestTVDevicePhantomSweep(t *testing.T) {
	fx := newPairingFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	now := time.Now()
	twoDaysAgo := now.Add(-48 * time.Hour)

	phantom := insertTVDevice(t, fx.pool, deviceSeed{
		LocationID: fx.loc, Label: "Never polled", PairedAt: &twoDaysAgo,
	})
	// Inside the day-long grace: this is the screen a human is pairing right
	// now, or one whose first poll is simply late. A ten-minute sweep would
	// delete it — this is the boundary that keeps a live handshake safe.
	justPaired := now.Add(-23 * time.Hour)
	fresh := insertTVDevice(t, fx.pool, deviceSeed{
		LocationID: fx.loc, Label: "Inside the day grace", PairedAt: &justPaired,
	})
	// Immunity: one poll ever, and the row is never a phantom again.
	seen := now.Add(-30 * time.Second)
	seenID := insertTVDevice(t, fx.pool, deviceSeed{
		LocationID: fx.loc, Label: "Polled once", PairedAt: &twoDaysAgo, LastSeen: &seen,
	})
	// Revocation is the audit trail, not a phantom.
	revoked := now.Add(-time.Hour)
	revokedID := insertTVDevice(t, fx.pool, deviceSeed{
		LocationID: fx.loc, Label: "Revoked", PairedAt: &twoDaysAgo, LastSeen: nil, RevokedAt: &revoked,
	})

	// A live pairing row must be outside this sweep's scope.
	pairing := fx.newCode(t, "666666", 5*time.Minute)

	deleted, err := fx.devices.SweepPhantoms(ctx)
	if err != nil {
		t.Fatalf("phantom sweep: %v", err)
	}
	if deleted != 1 {
		t.Errorf("phantom sweep deleted %d devices, want 1", deleted)
	}
	if got := fx.countRows(t, `SELECT count(*) FROM tv_devices WHERE id=$1`, phantom); got != 0 {
		t.Error("a device that never registered a poll survived past the day grace")
	}
	for name, id := range map[string]uuid.UUID{
		"device inside the day grace": fresh,
		"device with a last_seen":     seenID,
		"revoked device":              revokedID,
	} {
		if got := fx.countRows(t, `SELECT count(*) FROM tv_devices WHERE id=$1`, id); got != 1 {
			t.Errorf("%s was deleted by the phantom sweep", name)
		}
	}
	if _, ok := fx.state(t, pairing.ID); !ok {
		t.Error("the phantom sweep deleted a pairing row")
	}

	if deleted, err = fx.devices.SweepPhantoms(ctx); err != nil {
		t.Fatalf("second phantom sweep: %v", err)
	}
	if deleted != 0 {
		t.Errorf("idle phantom sweep deleted %d devices, want 0", deleted)
	}
}
