package handlers

import "testing"

func TestNormalizeBookingSort(t *testing.T) {
	cases := map[string]string{
		"":         "newest",
		"newest":   "newest",
		"latest":   "latest",
		"upcoming": "upcoming",
		"bogus":    "newest",
		"Latest":   "newest",
	}
	for in, want := range cases {
		if got := normalizeBookingSort(in); got != want {
			t.Errorf("normalizeBookingSort(%q) = %q, want %q", in, got, want)
		}
	}
}
