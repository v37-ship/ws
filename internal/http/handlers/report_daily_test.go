package handlers

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
)

// dailyWIB is the reporting clock: every daily bucket is a WIB calendar day, so
// the normalization tests pin `now` in that zone instead of the process's local
// one.
var dailyWIB = time.FixedZone("WIB", 7*60*60)

// TestNormalizeReportDate pins the ?date= contract: a real past or present WIB
// day is kept verbatim, while every malformed or future value normalizes to
// today in Asia/Jakarta. Future dates are refused because the daily view is an
// operating picture, not a forecast; a silent clamp is better than an empty
// panel for a mistyped link.
func TestNormalizeReportDate(t *testing.T) {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, dailyWIB)
	const today = "2026-11-24"
	cases := []struct{ in, want, why string }{
		{today, today, "today is allowed"},
		{"2026-11-23", "2026-11-23", "yesterday is allowed"},
		{"2024-02-29", "2024-02-29", "leap day parses"},
		{" 2026-11-23 ", "2026-11-23", "whitespace is trimmed"},
		{"2026-11-25", today, "tomorrow is refused"},
		{"2099-01-01", today, "a far-future date is refused"},
		{"2026-11-24T00:00:00Z", today, "a timestamp is not a date"},
		{"2026-13-01", today, "month 13 does not parse"},
		{"2026-02-30", today, "day 30 of February does not parse"},
		{"2026-1-2", today, "unpadded parts are not the canonical form"},
		{"", today, "missing param means today"},
		{"yesterday", today, "words are not dates"},
	}
	for _, tc := range cases {
		if got := normalizeReportDate(tc.in, now); got != tc.want {
			t.Errorf("normalizeReportDate(%q) = %q, want %q (%s)", tc.in, got, tc.want, tc.why)
		}
	}

	// Just after midnight WIB is still the 24th even though UTC is still on the
	// 23rd: "today" must be the WIB day the operator is standing in.
	early := time.Date(2026, 11, 24, 0, 5, 0, 0, dailyWIB)
	if got := early.UTC().Format("2006-01-02"); got != "2026-11-23" {
		t.Fatalf("fixture premise broken: 00:05 WIB is UTC %s, want 2026-11-23", got)
	}
	if got := normalizeReportDate("2026-11-24", early); got != "2026-11-24" {
		t.Errorf("WIB today at 00:05 = %q, want 2026-11-24", got)
	}
	if got := normalizeReportDate("2026-11-25", early); got != "2026-11-24" {
		t.Errorf("future day at 00:05 WIB = %q, want the WIB today", got)
	}
}

// dailyFixture is one seeded operating day for the payload/rendering tests.
func dailyFixture() domain.DailySummary {
	staffID := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	return domain.DailySummary{
		Date:      "2020-01-02", // safely in the past, so reportsData keeps it
		Completed: 7,
		WalkIns:   2,
		NoShows:   1,
		Cancelled: 4,
		CashIDR:   150_000,
		QRISIDR:   275_000,
		Upcoming: []domain.ReportLine{
			{
				BookingID:    uuid.MustParse("55555555-5555-5555-5555-555555555555"),
				StartAt:      time.Date(2020, 1, 2, 10, 0, 0, 0, dailyWIB),
				CustomerName: "Mom Lila", ServiceName: "Baby Massage",
				StaffID: staffID, StaffName: "Bidan Ayu", Status: domain.BookingConfirmed,
			},
			{
				BookingID:    uuid.MustParse("66666666-6666-6666-6666-666666666666"),
				StartAt:      time.Date(2020, 1, 2, 15, 30, 0, 0, dailyWIB),
				CustomerName: "Mom Sari", ServiceName: "Mom Massage",
				StaffID: staffID, StaffName: "Bidan Ayu", Status: domain.BookingConfirmed,
			},
		},
	}
}

// dailyTestInput builds the daily tab's payload from the shared report fixture,
// carrying the date and the service's daily read.
func dailyTestInput(date string, daily domain.DailySummary) reportsInput {
	in := reportTestInput("daily", 1)
	in.Date = date
	in.Daily = daily
	return in
}

// TestReportsDataDailyPanel pins the pure builder's half of the contract: the
// date rides in the payload, the counts/money are the service's output
// untouched, and the day arrows keep the tab and location while resetting the
// page.
func TestReportsDataDailyPanel(t *testing.T) {
	daily := dailyFixture()
	data := reportsData(dailyTestInput(daily.Date, daily))

	if got := data["Date"]; got != "2020-01-02" {
		t.Errorf("Date = %v, want 2020-01-02", got)
	}
	if got, ok := data["Daily"].(domain.DailySummary); !ok || got.Completed != 7 || got.QRISIDR != 275_000 {
		t.Errorf("Daily = %T %+v, want the service output unchanged", data["Daily"], data["Daily"])
	}

	prev := mustParseURL(t, data["DayPrevURL"].(string))
	if got := prev.Query().Get("date"); got != "2020-01-01" {
		t.Errorf("DayPrevURL date = %q, want 2020-01-01", got)
	}
	if got := prev.Query().Get("tab"); got != "daily" {
		t.Errorf("DayPrevURL tab = %q, want daily", got)
	}
	if got := prev.Query().Get("location_id"); got != "22222222-2222-2222-2222-222222222222" {
		t.Errorf("DayPrevURL lost the location: %q", got)
	}
	if got := prev.Query().Get("page"); got != "" {
		t.Errorf("DayPrevURL page = %q, want it dropped", got)
	}

	next := mustParseURL(t, data["DayNextURL"].(string))
	if got := next.Query().Get("date"); got != "2020-01-03" {
		t.Errorf("DayNextURL date = %q, want 2020-01-03", got)
	}

	// The month tab links must not carry a stale date into another tab.
	for _, tb := range data["Tabs"].([]reportTabLink) {
		if got := mustParseURL(t, tb.URL).Query().Get("date"); got != "" {
			t.Errorf("tab %s URL kept date=%q", tb.ID, got)
		}
	}
	if got := mustParseURL(t, data["PrevMonthURL"].(string)).Query().Get("date"); got != "" {
		t.Errorf("PrevMonthURL kept date=%q", got)
	}

	// Today is the upper bound: a future day is normalized away and the next
	// arrow disappears, while the Today jump is offered only when off-today.
	today := currentDate()
	atToday := reportsData(dailyTestInput(today, domain.DailySummary{Date: today}))
	if got := atToday["Date"]; got != today {
		t.Errorf("today Date = %v, want %v", got, today)
	}
	if got := atToday["DayNextURL"]; got != "" {
		t.Errorf("today DayNextURL = %q, want none", got)
	}
	if got := atToday["DayTodayURL"]; got != "" {
		t.Errorf("today DayTodayURL = %q, want none", got)
	}
	if got := data["DayTodayURL"]; got == "" {
		t.Error("a past day should offer the Today jump")
	}

	future := reportsData(dailyTestInput("2099-01-01", domain.DailySummary{Date: "2099-01-01"}))
	if got := future["Date"]; got != today {
		t.Errorf("future date Date = %v, want today %v", got, today)
	}
	if got := future["DayNextURL"]; got != "" {
		t.Errorf("future date DayNextURL = %q, want none", got)
	}
}

// dailyCardValue returns the rendered value inside the stat card carrying the
// given data-daily marker, so a template edit that swaps two cards fails.
func dailyCardValue(t *testing.T, out, kind string) string {
	t.Helper()
	marker := `data-daily="` + kind + `"`
	start := strings.Index(out, marker)
	if start < 0 {
		t.Fatalf("daily panel missing card %s", marker)
	}
	window := out[start:]
	if len(window) > 300 {
		window = window[:300]
	}
	return window
}

// TestReportsDailyPanelRenders executes the daily panel and reads the figures
// back out of the HTML: the date label, all four visit counts, cash/QRIS
// collected, and the upcoming rows in WIB time.
func TestReportsDailyPanelRenders(t *testing.T) {
	s := newReportTestServer(t)
	daily := dailyFixture()
	data := reportsData(dailyTestInput(daily.Date, daily))
	data["Lang"] = "en"
	data["Contact"] = portalPrintData("en")["Contact"]

	var buf bytes.Buffer
	if err := s.tpl.ExecuteTemplate(&buf, "portal_reports.html", data); err != nil {
		t.Fatalf("execute portal_reports.html: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"2020-01-02",   // the panel labels the day it describes
		"Rp 150.000",   // cash collected that day
		"Rp 275.000",   // QRIS collected that day
		"Mom Lila",     // upcoming customer
		"Mom Sari",     // second upcoming customer
		"Baby Massage", // upcoming service
		"Bidan Ayu",    // upcoming staff
		"10:00",        // first slot, WIB
		"15:30",        // second slot, WIB
		"Upcoming",     // section heading (EN copy)
		`name="date"`,  // the day picker stays on screen
	} {
		if !strings.Contains(out, want) {
			t.Errorf("daily panel missing %q", want)
		}
	}

	for kind, want := range map[string]string{
		"completed": ">7<",
		"walkins":   ">2<",
		"noshow":    ">1<",
		"cancelled": ">4<",
	} {
		if window := dailyCardValue(t, out, kind); !strings.Contains(window, want) {
			t.Errorf("daily card %s window missing %q: %q", kind, want, window)
		}
	}

	// The day picker cannot request a future day; the server would clamp it,
	// but the input itself should say so.
	if !strings.Contains(out, `max="`+currentDate()+`"`) {
		t.Errorf("day picker missing max=%q", currentDate())
	}

	// Every scrolling table on the page keeps the shared table-cards class.
	if cards, wraps := strings.Count(out, `class="table-cards"`), strings.Count(out, "overflow-x:auto;"); cards != wraps || cards == 0 {
		t.Errorf("table wrappers: %d table-cards for %d scroll wrappers, want one each", cards, wraps)
	}
}

// TestReportsDailyPanelEmptyCopy pins the honest empty state: a quiet day still
// renders the counts (zeros) and says there are no confirmed bookings left,
// rather than leaving a heading over a blank table.
func TestReportsDailyPanelEmptyCopy(t *testing.T) {
	for _, lang := range []string{"en", "id"} {
		s := newReportTestServer(t)
		data := reportsData(dailyTestInput("2020-01-02", domain.DailySummary{Date: "2020-01-02"}))
		data["Lang"] = lang
		data["Contact"] = portalPrintData(lang)["Contact"]

		var buf bytes.Buffer
		if err := s.tpl.ExecuteTemplate(&buf, "portal_reports.html", data); err != nil {
			t.Fatalf("%s: execute portal_reports.html: %v", lang, err)
		}
		out := buf.String()
		want := "No confirmed bookings left today."
		if lang == "id" {
			want = "Tidak ada booking terkonfirmasi tersisa hari ini."
		}
		if !strings.Contains(out, want) {
			t.Errorf("%s: daily empty state missing %q", lang, want)
		}
		if !strings.Contains(out, ">0<") {
			t.Errorf("%s: daily empty state should still show zero counts", lang)
		}
	}
}

// TestDailyTabFetchesItsOwnRead is a compile-time reminder that the daily
// payload is built from a different service call than the monthly ledger: the
// date and the Daily summary are independent of Month, so changing the month
// select cannot silently change the daily figures.
func TestDailyTabFetchesItsOwnRead(t *testing.T) {
	daily := dailyFixture()
	in := dailyTestInput(daily.Date, daily)
	in.Month = "2025-01"
	data := reportsData(in)
	if got := data["Month"]; got != "2025-01" {
		t.Errorf("Month = %v, want 2025-01", got)
	}
	if got := data["Date"]; got != daily.Date {
		t.Errorf("Date = %v, want %v (the daily day is not the month)", got, daily.Date)
	}
	if got := data["Daily"].(domain.DailySummary).Completed; got != daily.Completed {
		t.Errorf("Daily.Completed = %d, want %d", got, daily.Completed)
	}
	// Query carries no date, so the day links must still be built explicitly
	// rather than read back from the request.
	if got := data["DayPrevURL"].(string); !strings.Contains(got, "date=2020-01-01") {
		t.Errorf("DayPrevURL = %q, want date=2020-01-01", got)
	}
}
