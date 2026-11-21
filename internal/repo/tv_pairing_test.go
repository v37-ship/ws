package repo

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestTVPairingConsumeSQLIsAtomic pins the handshake's one critical statement:
// the code is claimed by a SINGLE UPDATE that re-checks "unconsumed and
// unexpired" in the same predicate it writes consumed_at with, and returns the
// row so the caller gets the location/device it just bound. Two staff members
// entering the same code must therefore produce exactly one winner.
func TestTVPairingConsumeSQLIsAtomic(t *testing.T) {
	q := tvPairingConsumeSQL
	if strings.Contains(q, ";") {
		t.Fatalf("consume must be one statement, got:\n%s", q)
	}
	for _, want := range []string{
		"UPDATE tv_pairings",
		"SET consumed_at=now(), location_id=$2",
		"code=$1",
		"consumed_at IS NULL",
		"expires_at>now()",
		"RETURNING",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("consume SQL missing %q:\n%s", want, q)
		}
	}
}

// TestTVPairingMissIsNotFound pins the miss contract Task 2 depends on: an
// UPDATE ... RETURNING that matched nothing (wrong code, already consumed,
// expired) surfaces as ErrNotFound, never as a raw pgx error or a nil row.
func TestTVPairingMissIsNotFound(t *testing.T) {
	p, err := tvPairingFromRow(rowErr{pgx.ErrNoRows})
	if !errors.Is(err, ErrNotFound) || p != nil {
		t.Fatalf("no-row consume = (%v, %v), want (nil, ErrNotFound)", p, err)
	}
	boom := errors.New("connection reset")
	if _, err := tvPairingFromRow(rowErr{boom}); !errors.Is(err, boom) {
		t.Errorf("other errors must pass through, got %v", err)
	}
}

// TestTVPairingSweepDeletesOnlyDeadRows pins the ticker's deletion contract
// (ruling #10). Two classes go: rows whose code can never be claimed again
// (expired), and claimed rows past a grace window long enough for the TV's last
// 4s poll to still read its pairing. Anything else — a live code, a just-claimed
// row — must survive the sweep.
func TestTVPairingSweepDeletesOnlyDeadRows(t *testing.T) {
	q := tvPairingSweepSQL
	if strings.Contains(q, ";") {
		t.Fatalf("sweep must be one statement, got:\n%s", q)
	}
	for _, want := range []string{
		"DELETE FROM tv_pairings",
		"expires_at < now()",
		"consumed_at < now() - interval '10 minutes'",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("sweep SQL missing %q:\n%s", want, q)
		}
	}
	if strings.Contains(q, "consumed_at IS NULL") {
		t.Errorf("sweep must not spare every unconsumed row forever:\n%s", q)
	}
}

// TestTVDeviceTouchLastSeenIsThrottled pins the reason the 30s poll can stamp
// liveness at all: the UPDATE carries its own threshold predicate, so a second
// stamp inside the window matches zero rows instead of rewriting the row every
// half minute. The no-op is the SQL's, not a caller-side flag that could drift.
func TestTVDeviceTouchLastSeenIsThrottled(t *testing.T) {
	q := tvDeviceTouchLastSeenSQL
	if strings.Contains(q, ";") {
		t.Fatalf("touch must be one statement, got:\n%s", q)
	}
	for _, want := range []string{
		"UPDATE tv_devices SET last_seen=now()",
		"WHERE id=$1",
		"(last_seen IS NULL OR last_seen < $2)",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("touch SQL missing %q:\n%s", want, q)
		}
	}
}

// rowErr is a pgx.Row that always fails its Scan, standing in for the driver
// result of an UPDATE that matched no rows (there is no DB in the test suite).
type rowErr struct{ err error }

func (r rowErr) Scan(...any) error { return r.err }
