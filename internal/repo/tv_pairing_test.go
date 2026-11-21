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

// rowErr is a pgx.Row that always fails its Scan, standing in for the driver
// result of an UPDATE that matched no rows (there is no DB in the test suite).
type rowErr struct{ err error }

func (r rowErr) Scan(...any) error { return r.err }
