package domain

import (
	"testing"
	"time"
)

// token is a realistic kiosk token: 32 random bytes, hex encoded (the same
// shape service.NewAccessToken mints for guest bookings).
const tvToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// TestTVTokenTokenOK pins the compare: an exact match is the only pass, an
// empty candidate never passes, and a length mismatch fails without leaking
// which prefix matched. Revocation is deliberately NOT this function's job
// (Active() is the separate gate), so a revoked row still matches its own
// token — a caller that forgets Active() must be the thing tests catch.
func TestTVTokenTokenOK(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		row  TVToken
		in   string
		want bool
	}{
		{"exact match", TVToken{Token: tvToken}, tvToken, true},
		{"wrong last byte", TVToken{Token: tvToken}, tvToken[:len(tvToken)-1] + "0", false},
		{"wrong first byte", TVToken{Token: tvToken}, "1" + tvToken[1:], false},
		{"empty candidate", TVToken{Token: tvToken}, "", false},
		{"empty row", TVToken{}, tvToken, false},
		{"truncated candidate", TVToken{Token: tvToken}, tvToken[:43], false},
		{"padded candidate", TVToken{Token: tvToken}, tvToken + "x", false},
		{"revoked row still matches its token", TVToken{Token: tvToken, RevokedAt: &now}, tvToken, true},
	}
	for _, tc := range tests {
		if got := tc.row.TokenOK(tc.in); got != tc.want {
			t.Errorf("%s: TokenOK = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestTVTokenActive pins the revocation gate the routes rely on.
func TestTVTokenActive(t *testing.T) {
	if !(TVToken{Token: tvToken}).Active() {
		t.Error("a fresh token must be active")
	}
	now := time.Now()
	if (TVToken{Token: tvToken, RevokedAt: &now}).Active() {
		t.Error("a revoked token must not be active")
	}
}
