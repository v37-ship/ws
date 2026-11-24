package service

import "testing"

// TestDemandRateBp pins the basis-point math behind the two ratios the demand
// block shows: the no-show rate (no-shows among finished visits) and the
// homecare share (homecare visits among the month's visits). Both are integer
// basis points — 1 bp = 0.01% — rounded half up, and a zero denominator is 0
// (the template renders "—" instead of a silent 0%, because "0 of 0" has no
// rate).
func TestDemandRateBp(t *testing.T) {
	cases := []struct {
		part, whole, want int
		why               string
	}{
		{0, 0, 0, "no denominator is not a rate"},
		{0, 8, 0, "an honest zero"},
		{1, 8, 1250, "1 of 8 = 12.50%"},
		{3, 8, 3750, "3 of 8 = 37.50%"},
		{1, 3, 3333, "3333.33 bp rounds down"},
		{2, 3, 6667, "6666.67 bp rounds up"},
		{1, 2, 5000, "half is exact"},
		{10, 10, 10000, "100%"},
		{1, 0, 0, "a part without a whole is undefined"},
	}
	for _, tc := range cases {
		if got := demandRateBp(tc.part, tc.whole); got != tc.want {
			t.Errorf("demandRateBp(%d, %d) = %d, want %d (%s)", tc.part, tc.whole, got, tc.want, tc.why)
		}
	}
}
