package handlers

import (
	"bytes"
	"log/slog"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"spa-booking/internal/config"
	"spa-booking/internal/domain"

	"github.com/google/uuid"
)

// TestNormalizeReportTab pins the tab whitelist: the four panels task 1 ships
// plus every hostile value an old bookmark or a typo can carry. Unknown input
// falls back to "revenue" — the ledger the page was before the tabs existed —
// so a stale link never lands on a blank panel.
func TestNormalizeReportTab(t *testing.T) {
	cases := []struct{ in, want string }{
		{"daily", "daily"},
		{"revenue", "revenue"},
		{"ops", "ops"},
		{"staff", "staff"},
		{"", "revenue"},
		{"Revenue", "revenue"},
		{" staff ", "revenue"},
		{"tab=ops", "revenue"},
		{"payouts", "revenue"},
	}
	for _, tc := range cases {
		if got := normalizeReportTab(tc.in); got != tc.want {
			t.Errorf("normalizeReportTab(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// reportFixtureLines builds 30 paid lines: 15 cash at Rp100.000 and 15 QRIS at
// Rp210.000, so the month's totals are distinctive and the table spans two
// 25-row pages.
func reportFixtureLines() []domain.ReportLine {
	staff := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	paidAt := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	lines := make([]domain.ReportLine, 0, 30)
	for i := 0; i < 30; i++ {
		method, amount := domain.PaymentMethodCash, int64(100_000)
		if i >= 15 {
			method, amount = domain.PaymentMethodQRIS, 210_000
		}
		lines = append(lines, domain.ReportLine{
			PaymentID: uuid.New(), BookingID: uuid.New(),
			AmountIDR: amount, PaymentMethod: method, PaidAt: paidAt.Add(time.Duration(i) * time.Hour),
			CustomerName: "Mom", ServiceName: "Baby Massage", StaffID: staff, StaffName: "Bidan Ayu",
			Status: domain.BookingCompleted,
		})
	}
	return lines
}

// reportFixtureRefunds is the recorded-refund side of the same fixture.
func reportFixtureRefunds() []domain.ReportRefund {
	return []domain.ReportRefund{
		{Refund: domain.Refund{AmountIDR: 50_000, CreatedAt: time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)}},
		{Refund: domain.Refund{AmountIDR: 25_000, CreatedAt: time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)}},
	}
}

// reportFixtureSummary computes the month summary from the fixtures with the
// service's own cash-basis definition (gross = Σ paid lines by method,
// refunds deducted in their recorded month). TestMonthlyReport* in the tagged
// integration suite pins ReportService.MonthlyReport against real SQL; this
// unit fixture pins what the handler does with its output.
func reportFixtureSummary() domain.ReportSummary {
	sum := domain.ReportSummary{Month: "2026-09"}
	for _, ln := range reportFixtureLines() {
		sum.GrossIDR += ln.AmountIDR
		sum.Count++
		if ln.PaymentMethod == domain.PaymentMethodCash {
			sum.CashIDR += ln.AmountIDR
		} else {
			sum.QRISIDR += ln.AmountIDR
		}
	}
	for _, rf := range reportFixtureRefunds() {
		sum.RefundIDR += rf.AmountIDR
		sum.RefundCnt++
	}
	sum.NetIDR = sum.GrossIDR - sum.RefundIDR
	return sum
}

// reportTestInput is one handler payload exactly as PortalReports assembles it
// from the service call.
func reportTestInput(tab string, page int) reportsInput {
	return reportsInput{
		Month:   "2026-09",
		Summary: reportFixtureSummary(),
		Lines:   reportFixtureLines(),
		Refunds: reportFixtureRefunds(),
		Tab:     tab,
		Page:    page,
		Demand:  demandFixture(),
		Years:   []string{"2026", "2025"},
		Locations: []domain.Location{
			{ID: uuid.MustParse("22222222-2222-2222-2222-222222222222"), Name: "Kayu Putih"},
		},
		Query: url.Values{
			"month":       {"2026-09"},
			"tab":         {tab},
			"location_id": {"22222222-2222-2222-2222-222222222222"},
		},
	}
}

// TestReportsDataSummaryIsServiceOutput pins the revenue regression: every
// figure the page shows comes straight from the service's ReportSummary, on
// every page of the transaction table. Nothing in the handler may recompute,
// round or re-derive money as the page gains tabs.
func TestReportsDataSummaryIsServiceOutput(t *testing.T) {
	want := reportFixtureSummary()
	// The fixture's arithmetic, spelled out: 15×100.000 cash + 15×210.000 QRIS.
	if want.GrossIDR != 4_650_000 || want.CashIDR != 1_500_000 ||
		want.QRISIDR != 3_150_000 || want.RefundIDR != 75_000 ||
		want.NetIDR != 4_575_000 || want.Count != 30 || want.RefundCnt != 2 {
		t.Fatalf("fixture summary drifted: %+v", want)
	}
	for _, page := range []int{1, 2} {
		data := reportsData(reportTestInput("revenue", page))
		got, ok := data["Summary"].(domain.ReportSummary)
		if !ok {
			t.Fatalf("page %d: Summary has type %T, want domain.ReportSummary", page, data["Summary"])
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("page %d: summary = %+v, want the service output %+v", page, got, want)
		}
		if data["Month"] != "2026-09" {
			t.Errorf("page %d: Month = %v, want 2026-09", page, data["Month"])
		}
	}
}

// TestReportsPageRendersSummaryValues executes the page and reads the money
// back out of the HTML, so a template edit that quietly swaps a field (e.g.
// printing Net where Gross belongs) fails here rather than in production.
func TestReportsPageRendersSummaryValues(t *testing.T) {
	s := newReportTestServer(t)
	data := reportsData(reportTestInput("revenue", 1))
	data["Lang"] = "en"
	data["Contact"] = portalPrintData("en")["Contact"]
	var buf bytes.Buffer
	if err := s.tpl.ExecuteTemplate(&buf, "portal_reports.html", data); err != nil {
		t.Fatalf("execute portal_reports.html: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Rp 4.650.000", // gross
		"−Rp 75.000",   // refunds, with the deduction sign
		"Rp 4.575.000", // net
		"Rp 1.500.000", // cash
		"Rp 3.150.000", // QRIS
		"(30×)",        // paid transaction count
	} {
		if !strings.Contains(out, want) {
			t.Errorf("reports page missing %q from the summary strip", want)
		}
	}
	// Every horizontally scrolling table on this page is a long one, so each
	// wrapper must carry the shared table-cards class (modern.css turns rows
	// into cards under 720px). A new bare wrapper would silently regress
	// phones back to panning a six-column table sideways.
	if cards, wraps := strings.Count(out, `class="table-cards"`), strings.Count(out, "overflow-x:auto;"); cards != wraps || cards == 0 {
		t.Errorf("table wrappers: %d table-cards for %d scroll wrappers, want one each", cards, wraps)
	}
}

// TestReportsRendersActiveTabOnly pins the shell contract: the active tab's
// panel is in the HTML, the other three are not. Rendering one panel (instead
// of stacking three hidden ones) is the smallest correct form — print and
// "view source" both then show exactly the view the operator chose, and the
// scoped print rule in modern.css is belt-and-braces for later stacking.
func TestReportsRendersActiveTabOnly(t *testing.T) {
	s := newReportTestServer(t)
	tabs := []string{"daily", "revenue", "ops", "staff"}
	for _, lang := range []string{"en", "id"} {
		for _, tab := range tabs {
			data := reportsSweepData(tab)
			data["Lang"] = lang
			data["Contact"] = portalPrintData(lang)["Contact"]
			var buf bytes.Buffer
			if err := s.tpl.ExecuteTemplate(&buf, "portal_reports.html", data); err != nil {
				t.Fatalf("execute portal_reports.html (%s/%s): %v", lang, tab, err)
			}
			out := buf.String()
			for _, other := range tabs {
				marker := `data-tab-panel="` + other + `"`
				if other == tab && !strings.Contains(out, marker) {
					t.Errorf("%s/%s: missing active panel marker %s", lang, tab, marker)
				}
				if other != tab && strings.Contains(out, marker) {
					t.Errorf("%s/%s: rendered inactive panel %s", lang, tab, marker)
				}
			}
			active := `data-tab="` + tab + `" aria-current="page"`
			if !strings.Contains(out, active) {
				t.Errorf("%s/%s: tab link not marked current (%s)", lang, tab, active)
			}
			// The shared UI-chrome scan sees one page render at a time, so the
			// other three panels would never be checked for emoji icons.
			chrome := stripHrefs(stripScripts(out))
			for _, e := range uiEmoji {
				if strings.Contains(chrome, e) {
					t.Errorf("%s/%s: panel chrome must not use emoji %q", lang, tab, e)
				}
			}
		}
	}
}

// TestReportsTransactionsPager covers the paging window and the URLs: 25 rows
// on page 1, the tail on page 2, and every link carrying the month and the
// active tab so a page flip never drops the operator back to another view.
func TestReportsTransactionsPager(t *testing.T) {
	first := reportsData(reportTestInput("revenue", 1))
	if got := len(first["Lines"].([]domain.ReportLine)); got != 25 {
		t.Errorf("page 1 rows = %d, want 25", got)
	}
	if got := first["LineTotal"]; got != 30 {
		t.Errorf("LineTotal = %v, want 30", got)
	}
	if got := first["Page"]; got != 1 {
		t.Errorf("Page = %v, want 1", got)
	}
	if got := first["TotalPages"]; got != 2 {
		t.Errorf("TotalPages = %v, want 2", got)
	}
	if got := first["PrevURL"]; got != "" {
		t.Errorf("page 1 PrevURL = %q, want empty", got)
	}
	next := mustParseURL(t, first["NextURL"].(string))
	if next.Path != "/portal/reports" {
		t.Errorf("NextURL path = %q, want /portal/reports", next.Path)
	}
	for k, want := range map[string]string{
		"month":       "2026-09",
		"tab":         "revenue",
		"location_id": "22222222-2222-2222-2222-222222222222",
		"page":        "2",
	} {
		if got := next.Query().Get(k); got != want {
			t.Errorf("NextURL %s = %q, want %q", k, got, want)
		}
	}

	second := reportsData(reportTestInput("revenue", 2))
	if got := len(second["Lines"].([]domain.ReportLine)); got != 5 {
		t.Errorf("page 2 rows = %d, want 5", got)
	}
	if got := second["NextURL"]; got != "" {
		t.Errorf("last page NextURL = %q, want empty", got)
	}
	prev := mustParseURL(t, second["PrevURL"].(string))
	if got := prev.Query().Get("tab"); got != "revenue" {
		t.Errorf("PrevURL tab = %q, want revenue", got)
	}
	if got := prev.Query().Get("month"); got != "2026-09" {
		t.Errorf("PrevURL month = %q, want 2026-09", got)
	}
	if got := prev.Query().Get("page"); got != "" {
		t.Errorf("PrevURL page = %q, want the clean first-page address", got)
	}

	// A hand-typed page past the end clamps to the last real page instead of
	// slicing past the lines the service returned.
	over := reportsData(reportTestInput("revenue", 9))
	if got := over["Page"]; got != 2 {
		t.Errorf("clamped Page = %v, want 2", got)
	}
	if got := len(over["Lines"].([]domain.ReportLine)); got != 5 {
		t.Errorf("clamped rows = %d, want 5", got)
	}
}

// TestReportsTabAndMonthLinksKeepContext pins the navigation contract: tab
// links reset the page but keep the month (and location), month arrows keep
// the active tab, and the CSV/print controls never inherit a stale page.
func TestReportsTabAndMonthLinksKeepContext(t *testing.T) {
	in := reportTestInput("ops", 2)
	data := reportsData(in)

	tabs, ok := data["Tabs"].([]reportTabLink)
	if !ok || len(tabs) != 4 {
		t.Fatalf("Tabs = %T %v, want 4 links", data["Tabs"], data["Tabs"])
	}
	for _, tb := range tabs {
		u := mustParseURL(t, tb.URL)
		if got := u.Query().Get("month"); got != "2026-09" {
			t.Errorf("tab %s URL month = %q, want 2026-09", tb.ID, got)
		}
		if got := u.Query().Get("tab"); got != tb.ID {
			t.Errorf("tab %s URL tab = %q, want %q", tb.ID, got, tb.ID)
		}
		if got := u.Query().Get("page"); got != "" {
			t.Errorf("tab %s URL page = %q, want it dropped", tb.ID, got)
		}
		if got := u.Query().Get("location_id"); got != "22222222-2222-2222-2222-222222222222" {
			t.Errorf("tab %s URL lost the location: %q", tb.ID, got)
		}
		if tb.Active != (tb.ID == "ops") {
			t.Errorf("tab %s Active = %v, want %v", tb.ID, tb.Active, tb.ID == "ops")
		}
	}

	prev := mustParseURL(t, data["PrevMonthURL"].(string))
	if got := prev.Query().Get("month"); got != "2026-08" {
		t.Errorf("PrevMonthURL month = %q, want 2026-08", got)
	}
	if got := prev.Query().Get("tab"); got != "ops" {
		t.Errorf("PrevMonthURL tab = %q, want the active tab ops", got)
	}
	if got := prev.Query().Get("page"); got != "" {
		t.Errorf("PrevMonthURL page = %q, want it dropped", got)
	}
	nextMonth := mustParseURL(t, data["NextMonthURL"].(string))
	if got := nextMonth.Query().Get("month"); got != "2026-10" {
		t.Errorf("NextMonthURL month = %q, want 2026-10", got)
	}
	if got := nextMonth.Query().Get("tab"); got != "ops" {
		t.Errorf("NextMonthURL tab = %q, want ops", got)
	}
}

// TestReportsDataNormalizesUnknownTab keeps the pure builder on the same
// whitelist as the handler: a caller passing garbage gets the revenue panel,
// never an unrecognised (blank) panel.
func TestReportsDataNormalizesUnknownTab(t *testing.T) {
	data := reportsData(reportTestInput("bogus", 1))
	if got := data["Tab"]; got != "revenue" {
		t.Errorf("Tab = %v, want revenue", got)
	}
	for _, tb := range data["Tabs"].([]reportTabLink) {
		if tb.Active != (tb.ID == "revenue") {
			t.Errorf("tab %s Active = %v, want only revenue active", tb.ID, tb.Active)
		}
	}
}

// reportSweepData is the reports page's renderable payload for the template
// sweeps. It carries one staff row with its detail lines and refund, so a
// sweep that renders the staff tab also executes both modals.
func reportsSweepData(tab string) pageData {
	staffID := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	paidAt := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	return reportsData(reportsInput{
		Month: "2026-09",
		Summary: domain.ReportSummary{
			Month: "2026-09", GrossIDR: 100_000, NetIDR: 100_000, Count: 1, CashIDR: 100_000,
			ServiceRows: []domain.NameTotal{{Name: "Baby Massage", Total: 100_000}},
			StaffRows: []domain.StaffRow{{
				StaffID: staffID, Name: "Bidan Ayu", Sessions: 1,
				UniqueCustomers: 1, GrossIDR: 100_000, NetIDR: 100_000, AvgIDR: 100_000,
				Services:    []domain.StaffServiceRow{{Name: "Baby Massage", Count: 1, Total: 100_000}},
				TopServices: []domain.StaffServiceRow{{Name: "Baby Massage", Count: 1, Total: 100_000}},
				Lines: []domain.ReportLine{{
					CustomerName: "Mom", ServiceName: "Baby Massage", StaffName: "Bidan Ayu",
					PaymentMethod: domain.PaymentMethodCash, AmountIDR: 100_000,
					PaidAt: paidAt, Status: domain.BookingConfirmed,
				}},
				Refunds: []domain.ReportRefund{{
					Refund:       domain.Refund{AmountIDR: 50_000, Reason: "cancel", CreatedBy: "CS", CreatedAt: paidAt},
					CustomerName: "Mom", ServiceName: "Baby Massage",
				}},
			}},
		},
		Lines: []domain.ReportLine{{
			CustomerName: "Mom", ServiceName: "Baby Massage", StaffName: "Bidan Ayu",
			PaymentMethod: domain.PaymentMethodCash, AmountIDR: 100_000,
			PaidAt: paidAt, Status: domain.BookingCompleted,
		}},
		Refunds:   reportFixtureRefunds(),
		Tab:       tab,
		Page:      1,
		Date:      dailyFixture().Date,
		Daily:     dailyFixture(),
		Demand:    demandFixture(),
		Years:     []string{"2026", "2025"},
		Locations: []domain.Location{{ID: uuid.MustParse("22222222-2222-2222-2222-222222222222"), Name: "Kayu Putih"}},
		Query:     url.Values{"month": {"2026-09"}, "tab": {tab}},
	})
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	if raw == "" {
		t.Fatal("expected a URL, got an empty string")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

// newReportTestServer parses the embedded templates with no services wired:
// the reports page only needs its template set to render fixtures.
func newReportTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	return s
}
