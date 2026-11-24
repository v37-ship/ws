package handlers

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"spa-booking/internal/domain"
)

// trendFixture is a six-month net series plus a comparable MTD read. The
// months end in the fixture month (2026-09) so the labels line up with
// reportTestInput's month.
func trendFixture() domain.MonthTrend {
	return domain.MonthTrend{
		PctBp:       1_234,
		AsOf:        "2026-09-15",
		OK:          true,
		CurrentIDR:  4_575_000,
		PreviousIDR: 4_070_000,
		Series: []domain.MonthNet{
			{Month: "2026-04", NetIDR: 1_200_000},
			{Month: "2026-05", NetIDR: 900_000},
			{Month: "2026-06", NetIDR: 1_500_000},
			{Month: "2026-07", NetIDR: 1_100_000},
			{Month: "2026-08", NetIDR: 4_070_000},
			{Month: "2026-09", NetIDR: 4_575_000},
		},
	}
}

// TestReportsDataTrend pins the payload: the trend rides through untouched and
// the revenue tab gets a numeric-only inline SVG wrapped as trusted HTML. The
// builder only receives int64 nets, so the wrapper is safe by construction.
func TestReportsDataTrend(t *testing.T) {
	in := reportTestInput("revenue", 1)
	in.Trend = trendFixture()
	data := reportsData(in)

	got, ok := data["Trend"].(domain.MonthTrend)
	if !ok {
		t.Fatalf("Trend has type %T, want domain.MonthTrend", data["Trend"])
	}
	if got.PctBp != 1_234 || got.AsOf != "2026-09-15" || !got.OK || len(got.Series) != 6 {
		t.Errorf("Trend = %+v, want the service read unchanged", got)
	}
	spark, ok := data["Spark"].(template.HTML)
	if !ok {
		t.Fatalf("Spark has type %T, want template.HTML", data["Spark"])
	}
	if !strings.HasPrefix(string(spark), "<svg") || !strings.Contains(string(spark), "<polyline") {
		t.Errorf("Spark is not an inline SVG: %s", spark)
	}
}

// TestDeltaPct pins the signed display formatter. It never prints a signed
// zero, rounds to one decimal of a percent (10 bp), and drops the sign when
// the magnitude rounds to zero (|bp| < 5, so 4 pins to "0.0%", not "+0.0%");
// 5 bp is the first magnitude that keeps a sign.
func TestDeltaPct(t *testing.T) {
	cases := map[int]string{
		0:       "0.0%",
		5_000:   "+50.0%",
		-2_000:  "-20.0%",
		10_000:  "+100.0%",
		-10_000: "-100.0%",
		5:       "+0.1%",
		-5:      "-0.1%",
		4:       "0.0%",
		1_234:   "+12.3%",
		-1_234:  "-12.3%",
	}
	for bp, want := range cases {
		if got := string(deltaPct(bp)); got != want {
			t.Errorf("deltaPct(%d) = %q, want %q", bp, got, want)
		}
	}
}

// reportTrendData is the page payload for a month, with the trend supplied the
// way PortalReports supplies it.
func reportTrendData(tab, month string, trend domain.MonthTrend) reportsInput {
	in := reportTestInput(tab, 1)
	in.Month = month
	in.Summary.Month = month
	in.Trend = trend
	in.Query["month"] = []string{month}
	return in
}

// renderReports executes the page and returns its HTML.
func renderReports(t *testing.T, lang string, data pageData) string {
	t.Helper()
	s := newReportTestServer(t)
	data["Lang"] = lang
	data["Contact"] = portalPrintData(lang)["Contact"]
	var buf bytes.Buffer
	if err := s.tpl.ExecuteTemplate(&buf, "portal_reports.html", data); err != nil {
		t.Fatalf("execute portal_reports.html: %v", err)
	}
	return buf.String()
}

// TestReportsDeltaLine pins the sticky strip's trend line on the current month:
// an up move is signed and green-marked, a down move signed and red-marked, and
// a fresh month renders the "first days" copy instead of a percentage. The line
// describes its own cutoff, so the number is never mistaken for a full month,
// and the two nets the percentage compares ride underneath it — a percentage
// without its amounts cannot say whether the move was Rp 50k or Rp 5M. The
// strip belongs to the three month tabs, so all of them must draw it.
func TestReportsDeltaLine(t *testing.T) {
	month := currentMonth()
	for _, tab := range []string{"revenue", "ops", "staff"} {
		up := reportsData(reportTrendData(tab, month, domain.MonthTrend{
			PctBp: 1_234, AsOf: month + "-05", OK: true,
			CurrentIDR: 4_575_000, PreviousIDR: 4_070_000,
		}))
		out := renderReports(t, "en", up)
		if !strings.Contains(out, `data-trend="delta"`) {
			t.Errorf("%s tab: missing the trend delta line", tab)
		}
		for _, want := range []string{
			"Net so far", "per 5 ", "+12.3%", "vs same day last month", "class=\"delta up\"",
			"now Rp 4.575.000 vs Rp 4.070.000",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%s tab: up trend render missing %q", tab, want)
			}
		}
	}

	down := reportsData(reportTrendData("revenue", month, domain.MonthTrend{
		PctBp: -2_000, AsOf: month + "-09", OK: true,
	}))
	if out := renderReports(t, "en", down); !strings.Contains(out, "-20.0%") || !strings.Contains(out, "class=\"delta down\"") {
		t.Errorf("down trend did not render a signed down move: %s", out)
	}

	fresh := reportsData(reportTrendData("revenue", month, domain.MonthTrend{
		PctBp: 0, AsOf: month + "-01", OK: false,
	}))
	freshOut := renderReports(t, "id", fresh)
	if !strings.Contains(freshOut, "Awal bulan") {
		t.Errorf("fresh month did not render its copy: %s", freshOut)
	}
	if strings.Contains(freshOut, "sejauh ini") {
		t.Errorf("fresh month must not render a percentage line: %s", freshOut)
	}
}

// TestReportsDeltaHiddenForPastMonths keeps the line honest: a complete month
// has no month-to-date comparison to make, so the strip shows no percentage.
func TestReportsDeltaHiddenForPastMonths(t *testing.T) {
	data := reportsData(reportTrendData("revenue", "2020-01", domain.MonthTrend{
		PctBp: 5_000, AsOf: "2020-01-31", OK: true,
	}))
	out := renderReports(t, "en", data)
	if strings.Contains(out, "vs same day last month") || strings.Contains(out, "+50.0%") {
		t.Errorf("past month rendered a month-to-date delta: %s", out)
	}
}

// TestReportsSparkline pins placement and labels: the revenue tab carries the
// inline SVG under the summary with one short month label per point; the spark
// is numeric-only markup (no user text to escape) and prints as plain SVG.
func TestReportsSparkline(t *testing.T) {
	month := currentMonth()
	data := reportsData(reportTrendData("revenue", month, trendFixture()))
	out := renderReports(t, "en", data)
	for _, want := range []string{
		`data-trend="spark"`,
		"Net, last 6 months",
		`class="spark"`,
		`class="spark-labels"`,
		">Apr<", ">Sep<",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("revenue sparkline missing %q", want)
		}
	}

	daily := reportsData(reportTrendData("daily", month, trendFixture()))
	if out := renderReports(t, "en", daily); strings.Contains(out, `data-trend="spark"`) {
		t.Errorf("sparkline leaked onto the daily tab: %s", out)
	}
}

// TestReportsSparklineEmptyState pins the builder's empty guard: a zero-length
// series produces no SVG at all. The template carries no empty-chart branch
// because MonthlyNetSeries always returns six entries (a read error is a 500,
// not a short series), so the only way here is a hand-built fixture.
func TestReportsSparklineEmptyState(t *testing.T) {
	month := currentMonth()
	in := reportTestInput("revenue", 1)
	in.Month = month
	in.Trend = domain.MonthTrend{AsOf: month + "-01", OK: false}
	data := reportsData(in)
	if got := data["Spark"]; got != template.HTML("") {
		t.Errorf("empty series produced a spark: %q", got)
	}
	if out := renderReports(t, "en", data); strings.Contains(out, `class="spark"`) {
		t.Errorf("empty series rendered an svg: %s", out)
	}
}

// TestReportsSparklineMonthsLocalized proves the labels are not hard-coded
// English month names: ID renders its own abbreviations ("Agu", "Okt") from the
// same series.
func TestReportsSparklineMonthsLocalized(t *testing.T) {
	month := currentMonth()
	trend := trendFixture()
	trend.Series = []domain.MonthNet{
		{Month: "2026-04", NetIDR: 1},
		{Month: "2026-09", NetIDR: 2},
	}
	data := reportsData(reportTrendData("revenue", month, trend))
	out := renderReports(t, "id", data)
	if !strings.Contains(out, ">Apr<") || !strings.Contains(out, ">Sep<") {
		t.Errorf("id labels should use r.ms.* abbreviations: %s", out)
	}
	trend.Series[0].Month = "2026-08" // "Agu" only exists in the ID dictionary
	data = reportsData(reportTrendData("revenue", month, trend))
	if out := renderReports(t, "id", data); !strings.Contains(out, ">Agu<") {
		t.Errorf("id label did not localize August: %s", out)
	}
}
