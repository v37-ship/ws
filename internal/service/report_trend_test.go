package service

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"spa-booking/internal/domain"
)

// TestMonthDelta pins the month-to-date comparison rule: compare the net
// accrued through the same day-of-month in both months, expressed in basis
// points of a percent (+5000 = +50.0%). A month whose previous counterpart has
// no positive baseline is not comparable at all — day 1 of a fresh month, or a
// month that only ever saw refunds — so ok is false and the page renders its
// "first days" copy instead of a meaningless 100%.
func TestMonthDelta(t *testing.T) {
	cases := []struct {
		name    string
		current int64
		prev    int64
		month   string
		day     int
		wantBp  int
		wantAs  string
		wantOK  bool
	}{
		{"up", 1_500_000, 1_000_000, "2026-09", 15, 5_000, "2026-09-15", true},
		{"down", 800_000, 1_000_000, "2026-11", 24, -2_000, "2026-11-24", true},
		{"flat", 1_000_000, 1_000_000, "2026-09", 10, 0, "2026-09-10", true},
		{"current zero", 0, 1_000_000, "2026-09", 12, -10_000, "2026-09-12", true},
		{"half up rounding", 20_001, 20_000, "2026-09", 9, 1, "2026-09-09", true},
		{"five percent", 2_100_000, 2_000_000, "2026-09", 9, 500, "2026-09-09", true},
		{"one third rounds", 4, 3, "2026-09", 8, 3_333, "2026-09-08", true},
		{"fresh month", 0, 0, "2026-11", 1, 0, "2026-11-01", false},
		{"negative baseline", 100_000, -50_000, "2026-11", 3, 0, "2026-11-03", false},
		{"day past month end clamps", 1_000_000, 500_000, "2026-09", 31, 10_000, "2026-09-30", true},
		{"day below one clamps", 1_000_000, 500_000, "2026-02", 0, 10_000, "2026-02-01", true},
		{"bad month", 1_000_000, 500_000, "nope", 5, 10_000, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cur := domain.ReportSummary{Month: tc.month, NetIDR: tc.current}
			prev := domain.ReportSummary{Month: "2026-08", NetIDR: tc.prev}
			bp, asOf, ok := MonthDelta(cur, prev, tc.day)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
			if bp != tc.wantBp {
				t.Errorf("pctBp = %d, want %d", bp, tc.wantBp)
			}
			if asOf != tc.wantAs {
				t.Errorf("asOf = %q, want %q", asOf, tc.wantAs)
			}
		})
	}
}

// sparkPoints reads the polyline coordinates back out of the builder's SVG so
// the assertions are about geometry, not about string shape.
func sparkPoints(t *testing.T, svg string) [][2]int {
	t.Helper()
	m := regexp.MustCompile(`points="([^"]+)"`).FindStringSubmatch(svg)
	if m == nil {
		t.Fatalf("sparkline has no polyline points: %s", svg)
	}
	var pts [][2]int
	for _, pair := range strings.Fields(m[1]) {
		var x, y int
		if _, err := fmt.Sscanf(pair, "%d,%d", &x, &y); err != nil {
			t.Fatalf("bad point %q in %q: %v", pair, m[1], err)
		}
		pts = append(pts, [2]int{x, y})
	}
	return pts
}

// TestSparklineSVGNormalization pins the geometry contract: the series max
// reaches the top padding, the min reaches the bottom padding, coordinates are
// strictly increasing x, and the line spans the full width. Negative nets are
// legal (a month can refund more than it took), so the map shifts by min rather
// than assuming a zero floor.
func TestSparklineSVGNormalization(t *testing.T) {
	svg := SparklineSVG([]int64{0, 100, 50, 200, 150, 300}, 560, 72)
	if !strings.Contains(svg, `viewBox="0 0 560 72"`) {
		t.Errorf("svg viewBox missing: %s", svg)
	}
	pts := sparkPoints(t, svg)
	if len(pts) != 6 {
		t.Fatalf("points = %d, want one per value", len(pts))
	}
	if pts[0][0] != 2 || pts[5][0] != 558 {
		t.Errorf("x span = %d..%d, want 2..558", pts[0][0], pts[5][0])
	}
	for i := 1; i < len(pts); i++ {
		if pts[i][0] <= pts[i-1][0] {
			t.Errorf("x not increasing at %d: %d <= %d", i, pts[i][0], pts[i-1][0])
		}
	}
	if pts[5][1] != 2 {
		t.Errorf("max value y = %d, want the top padding 2", pts[5][1])
	}
	if pts[0][1] != 70 {
		t.Errorf("min value y = %d, want the bottom padding 70", pts[0][1])
	}

	// Negative floor: -100 → bottom, +100 → top, 0 exactly in the middle.
	neg := sparkPoints(t, SparklineSVG([]int64{-100, 0, 100}, 200, 64))
	wantY := []int{62, 32, 2}
	for i, want := range wantY {
		if neg[i][1] != want {
			t.Errorf("negative series point %d y = %d, want %d", i, neg[i][1], want)
		}
	}

	// All values equal: one line through the vertical middle (no divide by a
	// zero span, no snap to an edge).
	flat := sparkPoints(t, SparklineSVG([]int64{7, 7, 7, 7}, 300, 80))
	for i, p := range flat {
		if p[1] != 40 {
			t.Errorf("all-equal point %d y = %d, want mid 40", i, p[1])
		}
	}

	// A single value has no span and no second x to interpolate; render it as
	// a visible flat line at the middle rather than an invisible dot.
	single := sparkPoints(t, SparklineSVG([]int64{42}, 300, 80))
	if len(single) != 2 || single[0][1] != 40 || single[1][1] != 40 {
		t.Errorf("single value points = %v, want two mid points", single)
	}
}

// TestSparklineSVGEmpty pins the empty/all-zero state: a flat baseline at the
// bottom (zero literally reads as the floor) spanning the width.
func TestSparklineSVGEmpty(t *testing.T) {
	for name, series := range map[string][]int64{
		"nil":   nil,
		"empty": {},
		"zeros": {0, 0, 0, 0, 0, 0},
	} {
		t.Run(name, func(t *testing.T) {
			svg := SparklineSVG(series, 560, 72)
			pts := sparkPoints(t, svg)
			if len(pts) != 2 {
				t.Fatalf("points = %d, want the flat baseline", len(pts))
			}
			if pts[0][1] != 70 || pts[1][1] != 70 {
				t.Errorf("baseline y = %d..%d, want 70..70", pts[0][1], pts[1][1])
			}
		})
	}
}

// TestSparklineSVGGuards covers the hostile dimensions and the no-float rule:
// non-positive width/height fall back to defaults, and the integer-only math
// can never emit NaN or Inf into the page.
func TestSparklineSVGGuards(t *testing.T) {
	for _, dims := range [][2]int{{0, 0}, {-10, -1}, {0, 72}, {560, 0}} {
		svg := SparklineSVG([]int64{1, 2, 3}, dims[0], dims[1])
		if !strings.HasPrefix(svg, "<svg") {
			t.Fatalf("dims %v: not an SVG: %s", dims, svg)
		}
		for _, bad := range []string{"NaN", "Inf", "-Inf"} {
			if strings.Contains(svg, bad) {
				t.Errorf("dims %v: svg contains %s: %s", dims, bad, svg)
			}
		}
	}
	svg := SparklineSVG([]int64{1, 2, 3}, 0, 0)
	if !strings.Contains(svg, `viewBox="0 0 320 64"`) {
		t.Errorf("zero dimensions did not fall back to 320x64: %s", svg)
	}
}
