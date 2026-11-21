package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/google/uuid"
)

// tvDeviceSecret is a realistic device secret: 32 random bytes hex, the same
// shape the pairing's pending key has (it IS the pending key: the portal
// hashes it into the device row, the TV keeps it in its cookie).
const tvDeviceSecret = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

// sha256Hex mirrors what the portal writes into tv_devices.secret_hash.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestTVDeviceSecretOK pins the hash contract (ruling #9): the stored value is
// sha256 hex, the comparison happens over the hash in constant time, and a
// row that (mistakenly) stores the plaintext never authenticates — that would
// be the tv_tokens pattern this credential deliberately does not follow.
func TestTVDeviceSecretOK(t *testing.T) {
	hash := sha256Hex(tvDeviceSecret)
	tests := []struct {
		name string
		row  TVDevice
		in   string
		want bool
	}{
		{"exact secret", TVDevice{SecretHash: hash}, tvDeviceSecret, true},
		{"wrong secret", TVDevice{SecretHash: hash}, tvDeviceSecret[:63] + "0", false},
		{"empty candidate", TVDevice{SecretHash: hash}, "", false},
		{"empty stored hash", TVDevice{}, tvDeviceSecret, false},
		{"plaintext row is not a hash", TVDevice{SecretHash: tvDeviceSecret}, tvDeviceSecret, false},
		{"truncated candidate", TVDevice{SecretHash: hash}, tvDeviceSecret[:62], false},
		{"padded candidate", TVDevice{SecretHash: hash}, tvDeviceSecret + "x", false},
	}
	for _, tc := range tests {
		if got := tc.row.SecretOK(tc.in); got != tc.want {
			t.Errorf("%s: SecretOK = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestTVDeviceActive pins the revocation gate: a revoked device is dead
// immediately, independent of the secret compare (mirroring TVToken.Active).
func TestTVDeviceActive(t *testing.T) {
	if !(TVDevice{SecretHash: sha256Hex(tvDeviceSecret)}).Active() {
		t.Error("a fresh device must be active")
	}
	now := time.Now()
	if (TVDevice{RevokedAt: &now}).Active() {
		t.Error("a revoked device must not be active")
	}
}

// TestTVPairingLive pins the handshake's gate: a row is claimable only while
// it is unconsumed AND unexpired. Either condition alone ends the code's life.
func TestTVPairingLive(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		row  TVPairing
		want bool
	}{
		{"fresh", TVPairing{ExpiresAt: now.Add(time.Minute)}, true},
		{"consumed", TVPairing{ExpiresAt: now.Add(time.Minute), ConsumedAt: now}, false},
		{"expired", TVPairing{ExpiresAt: now.Add(-time.Second)}, false},
		{"zero expiry", TVPairing{}, false},
		{"consumed and expired", TVPairing{ExpiresAt: now.Add(-time.Second), ConsumedAt: now}, false},
	}
	for _, tc := range tests {
		if got := tc.row.Live(); got != tc.want {
			t.Errorf("%s: Live = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestTVDeviceCookie pins the wire shape the pair-state poll mints and the
// unified chain parses: the device id, a dot, then the secret. A malformed
// value must fail closed rather than authenticate a zero device.
func TestTVDeviceCookie(t *testing.T) {
	id := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	raw := TVDeviceCookie(id, tvDeviceSecret)
	if got, want := raw, id.String()+"."+tvDeviceSecret; got != want {
		t.Fatalf("TVDeviceCookie = %q, want %q", got, want)
	}
	gotID, gotSecret, ok := ParseTVDeviceCookie(raw)
	if !ok || gotID != id || gotSecret != tvDeviceSecret {
		t.Errorf("ParseTVDeviceCookie(%q) = (%s, %q, %v), want (%s, %q, true)", raw, gotID, gotSecret, ok, id, tvDeviceSecret)
	}
	// A secret containing a dot keeps it: only the first dot is the separator.
	gotID, gotSecret, ok = ParseTVDeviceCookie(id.String() + ".a.b")
	if !ok || gotID != id || gotSecret != "a.b" {
		t.Errorf("dotted secret split wrongly: (%s, %q, %v)", gotID, gotSecret, ok)
	}
	for _, bad := range []string{
		"",                // nothing
		id.String(),       // no secret
		id.String() + ".", // empty secret
		".secret",         // no id
		"not-a-uuid.secret",
	} {
		if _, _, ok := ParseTVDeviceCookie(bad); ok {
			t.Errorf("ParseTVDeviceCookie(%q) accepted a malformed value", bad)
		}
	}
}
