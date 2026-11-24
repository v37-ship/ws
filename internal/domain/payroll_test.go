package domain

import "testing"

// The payroll validators mirror the migration's CHECK constraints; handlers
// validate before insert so the user sees a field error rather than a 500.
func TestPayrollValidators(t *testing.T) {
	cadences := map[string]bool{
		CompCadenceWeekly: true, CompCadenceMonthly: true,
		"daily": false, "": false, "Weekly": false,
	}
	for cadence, want := range cadences {
		if got := ValidCadence(cadence); got != want {
			t.Errorf("ValidCadence(%q) = %v, want %v", cadence, got, want)
		}
	}

	bases := map[string]bool{
		CompBaseList: true, CompBaseNet: true,
		"gross": false, "": false, "List": false,
	}
	for base, want := range bases {
		if got := ValidCommissionBase(base); got != want {
			t.Errorf("ValidCommissionBase(%q) = %v, want %v", base, got, want)
		}
	}

	percents := []struct {
		bp   int
		want bool
	}{{0, true}, {2500, true}, {10_000, true}, {-1, false}, {10_001, false}}
	for _, tc := range percents {
		if got := ValidPercentBp(tc.bp); got != tc.want {
			t.Errorf("ValidPercentBp(%d) = %v, want %v", tc.bp, got, tc.want)
		}
	}
}
