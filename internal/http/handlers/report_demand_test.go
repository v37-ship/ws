package handlers

import (
	"bytes"
	"strings"
	"testing"

	"spa-booking/internal/domain"
)

// demandFixture is one month's business-health read with every metric distinct
// enough that a swapped card or a lost bucket fails the render assertions:
// 12 made / 9 paid / 2 cancelled / 3 no-shows in the funnel, 11 visits split
// 6/2/2/1 by source, 2 homecare, and 3 no-shows among 10 finished visits.
func demandFixture() domain.ReportDemand {
	return domain.ReportDemand{
		GrossPaid:       3_250_000,
		BookingsCreated: 12,
		BookingsPaid:    9,
		Cancelled:       2,
		NoShows:         3,
		NoShowVisits:    3,
		TerminalVisits:  10,
		NoShowRateBp:    3000,
		Visits:          11,
		SourceWeb:       6,
		SourceWalkin:    2,
		SourceWhatsapp:  2,
		SourcePhone:     1,
		HomecareVisits:  2,
		HomecareShareBp: 1818,
		PromoDiscIDR:    125_000,
		TransportIDR:    45_000,
	}
}

// demandTestInput is the ops tab's payload: the shared revenue fixture with the
// service's demand read attached.
func demandTestInput(demand domain.ReportDemand) reportsInput {
	in := reportTestInput("ops", 1)
	in.Demand = demand
	return in
}

// demandCardWindow finds one demand card in the rendered HTML so an assertion
// cannot be satisfied by a number that belongs to a different metric.
func demandCardWindow(t *testing.T, out, kind string) string {
	t.Helper()
	marker := `data-demand="` + kind + `"`
	start := strings.Index(out, marker)
	if start < 0 {
		t.Fatalf("demand block missing card %s", marker)
	}
	window := out[start:]
	if len(window) > 400 {
		window = window[:400]
	}
	return window
}

// TestReportsDataDemand pins the pure builder's half: the demand read rides in
// the payload untouched (the handler copies GrossPaid from the month summary and
// never recomputes it), and the ops tab is the only tab that shows it.
func TestReportsDataDemand(t *testing.T) {
	demand := demandFixture()
	data := reportsData(demandTestInput(demand))
	got, ok := data["Demand"].(domain.ReportDemand)
	if !ok {
		t.Fatalf("Demand has type %T, want domain.ReportDemand", data["Demand"])
	}
	if got != demand {
		t.Errorf("Demand = %+v, want the service output unchanged %+v", got, demand)
	}

	// The demand struct is not revenue: the summary strip keeps the service's
	// own gross, and nothing in the builder rewrites it from Demand.GrossPaid.
	if summary := data["Summary"].(domain.ReportSummary); summary.GrossIDR == demand.GrossPaid {
		t.Fatalf("fixture premise broken: summary gross and demand gross must differ to prove no coupling")
	}
}

// TestReportsDemandPanelRenders executes the ops panel and reads every group
// back out of the HTML: the funnel counts, the paid gross, the no-show rate
// with its denominator, the source mix, homecare share, and the two money
// snapshots.
func TestReportsDemandPanelRenders(t *testing.T) {
	s := newReportTestServer(t)
	data := reportsData(demandTestInput(demandFixture()))
	data["Lang"] = "en"
	data["Contact"] = portalPrintData("en")["Contact"]

	var buf bytes.Buffer
	if err := s.tpl.ExecuteTemplate(&buf, "portal_reports.html", data); err != nil {
		t.Fatalf("execute portal_reports.html: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"Business health", // panel heading
		"Funnel",          // funnel group heading
		"Visit mix",       // mix group heading
		"the no-show rate follows the visit date", // the card's basis, not created_at
		"Rp 3.250.000",            // paid gross, from the month summary
		"30.0%",                   // no-show rate from 3000 bp
		"3 of 10 finished visits", // the rate's denominator, labelled
		"18.1%",                   // homecare share from 1818 bp
		"2 of 11 visits",          // the share's denominator
		"Rp 125.000",              // promo discounts
		"Rp 45.000",               // transport fees
	} {
		if !strings.Contains(out, want) {
			t.Errorf("demand panel missing %q", want)
		}
	}

	for kind, want := range map[string]string{
		"made":   ">12<",
		"paid":   ">9<",
		"canc":   ">2<",
		"noshow": ">3<",
		"visits": ">11<",
		"web":    ">6<",
		"walk":   ">2<",
		"wa":     ">2<",
		"phone":  ">1<",
	} {
		if window := demandCardWindow(t, out, kind); !strings.Contains(window, want) {
			t.Errorf("demand card %s window missing %q: %q", kind, want, window)
		}
	}

	// The retired placeholder must be gone from the page now that the panel is
	// real (the key itself is deleted with this task).
	for _, gone := range []string{"r.tab.soon", "coming in the next update"} {
		if strings.Contains(out, gone) {
			t.Errorf("demand panel still carries the retired placeholder %q", gone)
		}
	}
}

// TestReportsDemandEmptyState pins the honest zero: a month with no rows still
// renders every label with an explicit 0, and the two ratios show "—" instead
// of inventing 0% from a zero denominator.
func TestReportsDemandEmptyState(t *testing.T) {
	for _, lang := range []string{"en", "id"} {
		s := newReportTestServer(t)
		data := reportsData(demandTestInput(domain.ReportDemand{}))
		data["Lang"] = lang
		data["Contact"] = portalPrintData(lang)["Contact"]

		var buf bytes.Buffer
		if err := s.tpl.ExecuteTemplate(&buf, "portal_reports.html", data); err != nil {
			t.Fatalf("%s: execute portal_reports.html: %v", lang, err)
		}
		out := buf.String()

		if !strings.Contains(out, ">0<") {
			t.Errorf("%s: empty demand block should still show zero counts", lang)
		}
		if got := strings.Count(out, "—"); got < 2 {
			t.Errorf("%s: empty demand block should show — for both undefined ratios, got %d", lang, got)
		}
		empty := "Nothing booked or scheduled in this month yet."
		noRate := "no finished visits yet"
		// The no-show rate sits inside the created-date funnel panel but is
		// counted by visit date; its basis line must say so.
		fbasis := "the no-show rate follows the visit date"
		if lang == "id" {
			empty = "Belum ada booking atau kunjungan di bulan ini."
			noRate = "belum ada kunjungan selesai"
			fbasis = "rasio tidak hadir mengikuti tanggal kunjungan"
		}
		if !strings.Contains(out, fbasis) {
			t.Errorf("%s: funnel basis line missing the no-show rate's visit basis %q", lang, fbasis)
		}
		if !strings.Contains(out, empty) {
			t.Errorf("%s: empty demand block missing %q", lang, empty)
		}
		if !strings.Contains(out, noRate) {
			t.Errorf("%s: empty demand block missing the no-rate explanation %q", lang, noRate)
		}
		// The labels stay: zeros are explicit, never blank cells.
		labels := []string{"Bookings made", "Visits", "Homecare share", "No-show rate"}
		if lang == "id" {
			labels = []string{"Booking dibuat", "Kunjungan", "Porsi homecare", "Rasio tidak hadir"}
		}
		for _, label := range labels {
			if !strings.Contains(out, label) {
				t.Errorf("%s: empty demand block missing label %q", lang, label)
			}
		}
	}
}

// TestReportsDemandPaidOnlyMonthHasNoEmptyCopy pins the empty predicate: a
// month whose paid money landed here while neither the created-date funnel nor
// the visit-date mix has rows is not empty. "Nothing booked or scheduled"
// would contradict the paid count and gross on the same panel.
func TestReportsDemandPaidOnlyMonthHasNoEmptyCopy(t *testing.T) {
	d := domain.ReportDemand{BookingsPaid: 4, GrossPaid: 800_000}
	data := reportsData(demandTestInput(d))
	out := renderReports(t, "en", data)

	for _, gone := range []string{
		"Nothing booked or scheduled in this month yet.",
		"Belum ada booking atau kunjungan di bulan ini.",
	} {
		if strings.Contains(out, gone) {
			t.Errorf("paid-only month rendered the empty copy %q", gone)
		}
	}
	if window := demandCardWindow(t, out, "paid"); !strings.Contains(window, ">4<") {
		t.Errorf("paid card did not show the paid count: %q", window)
	}
	if !strings.Contains(out, "Rp 800.000") {
		t.Error("paid-only month did not show its gross")
	}
}

// TestPercentBpFormat pins the display formatter: basis points render as one
// decimal of a percent and clamp at zero, so a template edit cannot print a raw
// "3000" where a rate belongs.
func TestPercentBpFormat(t *testing.T) {
	if got := percentBp(0); got != "0.0%" {
		t.Errorf("percentBp(0) = %q, want 0.0%%", got)
	}
	if got := percentBp(3000); got != "30.0%" {
		t.Errorf("percentBp(3000) = %q, want 30.0%%", got)
	}
	if got := percentBp(1818); got != "18.1%" {
		t.Errorf("percentBp(1818) = %q, want 18.1%%", got)
	}
	if got := percentBp(10000); got != "100.0%" {
		t.Errorf("percentBp(10000) = %q, want 100.0%%", got)
	}
	if got := percentBp(-5); got != "0.0%" {
		t.Errorf("percentBp(-5) = %q, want 0.0%%", got)
	}
}
