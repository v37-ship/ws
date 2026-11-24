package handlers

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"spa-booking/internal/config"
	"spa-booking/internal/domain"
	"spa-booking/internal/i18n"
	"spa-booking/internal/service"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// The payroll page reads one computed report and one paid-snapshot lookup per
// row. Both are small interfaces so the page can be driven end-to-end through
// the real router without a database: the salary math is Task 2's unit-tested
// service, and what this file pins is the staff gate, the period navigation,
// and the two renderings (HTML + CSV).

type fakePayrollReporter struct {
	report service.PayrollReport
	err    error
	kind   string
	start  time.Time
	calls  int
}

func (f *fakePayrollReporter) Report(_ context.Context, kind string, start time.Time) (service.PayrollReport, error) {
	f.calls++
	f.kind, f.start = kind, start
	return f.report, f.err
}

type fakePayoutStore struct {
	byKey     map[string]*domain.StaffPayout
	createErr error
	created   []domain.StaffPayout
	calls     int
	logs      *bytes.Buffer
}

func (f *fakePayoutStore) ForPeriod(_ context.Context, staffID uuid.UUID, kind string, periodStart time.Time) (*domain.StaffPayout, error) {
	f.calls++
	if f.byKey == nil {
		return nil, nil
	}
	return f.byKey[payrollPayoutKey(staffID, kind, periodStart)], nil
}

func (f *fakePayoutStore) Create(_ context.Context, p *domain.StaffPayout) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, *p)
	return nil
}

func payrollPayoutKey(staffID uuid.UUID, kind string, start time.Time) string {
	return staffID.String() + "|" + kind + "|" + start.In(payrollWIB).Format(payrollDayLayout)
}

// newPayrollServer wires the real router with the fake reads, the same shape
// admin_routes_test uses for its URL pins.
func newPayrollServer(t *testing.T, rep payrollReporter, payouts payoutStore) http.Handler {
	t.Helper()
	logs := &bytes.Buffer{}
	if store, ok := payouts.(*fakePayoutStore); ok {
		store.logs = logs
	}
	s, err := New(config.Config{}, slog.New(slog.NewTextHandler(logs, nil)), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	s.payroll = rep
	s.payouts = payouts
	r := chi.NewRouter()
	s.Routes(r)
	return r
}

func payrollTestWIB() *time.Location { return time.FixedZone("WIB", 7*3600) }

func payrollTestPeriod(kind string) service.PayrollPeriod {
	start := time.Date(2026, 11, 23, 0, 0, 0, 0, payrollTestWIB())
	if kind == service.PayrollKindMonth {
		start = time.Date(2026, 11, 1, 0, 0, 0, 0, payrollTestWIB())
	}
	return service.PayrollPeriod{Kind: kind, Start: start, End: start.AddDate(0, 0, 6)}
}

// payrollTestReport is the representative period: a normal commission row with
// a visit, a missing-rate row, and a monthly-fixed row whose fixed the week
// view omits (the "counted on the month" note).
func payrollTestReport(kind string) (service.PayrollReport, []payrollRowView) {
	period := payrollTestPeriod(kind)
	ayu := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	bima := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	citra := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	paidAt := time.Date(2026, 11, 30, 10, 0, 0, 0, payrollTestWIB())

	report := service.PayrollReport{
		Period:             period,
		MissingRate:        1,
		TotalGrossIDR:      550_000,
		TotalRefundIDR:     50_000,
		TotalCommissionIDR: 165_000,
		TotalFixedIDR:      0,
		TotalIDR:           165_000,
	}
	report.Rows = []service.PayrollRow{
		{
			StaffID: ayu, StaffName: "Ayu", Sessions: 2,
			GrossIDR: 300_000, RefundIDR: 50_000, NetIDR: 250_000,
			PercentBp: 3000, Base: domain.CompBaseNet,
			CommissionIDR: 75_000, FixedIDR: 0, TotalIDR: 75_000,
			Visits: []service.PayrollVisit{{
				BookingID: uuid.New(), StartAt: period.Start.Add(10 * time.Hour),
				CustomerName: "Sari", ServiceName: "Pijat Balinese",
				PriceIDR: 150_000, BaseIDR: 150_000,
			}},
			RefundLines: []service.PayrollRefundLine{{
				RefundID: uuid.New(), CreatedAt: period.Start.Add(48 * time.Hour),
				AmountIDR: 50_000, Reason: "late arrival",
			}},
		},
		{
			StaffID: bima, StaffName: "Bima", Sessions: 1,
			GrossIDR: 150_000, NetIDR: 150_000,
			RateMissing: true, Base: "",
		},
		{
			StaffID: citra, StaffName: "Citra", Sessions: 1,
			GrossIDR: 100_000, NetIDR: 100_000,
			PercentBp: 9000, Base: domain.CompBaseList,
			CommissionIDR: 90_000, TotalIDR: 90_000,
			FixedOnMonth: kind == service.PayrollKindWeek,
		},
	}
	// The stored period vocabulary is weekly/monthly; the page's view kind is
	// week/month. The fixture must speak the stored one, or it teaches the exact
	// confusion the mark-paid mapping exists to prevent.
	payoutKind := domain.PeriodWeekly
	if period.Kind == service.PayrollKindMonth {
		payoutKind = domain.PeriodMonthly
	}
	rows := []payrollRowView{
		{PayrollRow: report.Rows[0], Paid: &domain.StaffPayout{
			StaffID: ayu, PeriodKind: payoutKind, PeriodStart: period.Start,
			TotalIDR: 75_000, Note: "transfer BCA", PaidAt: paidAt,
		}},
		{PayrollRow: report.Rows[1]},
		{PayrollRow: report.Rows[2]},
	}
	return report, rows
}

func adminGet(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	return asUser(h, path, domain.RoleAdmin)
}

// payrollContactData is the footer's contact block for a direct template
// render (the HTTP path fills it from config in render()).
func payrollContactData() pageData {
	return pageData{
		"WhatsApp": "62800", "Email": "a@b.c", "Address": "addr", "Hours": "hours",
		"Maps": "https://maps.example/", "Instagram": "https://instagram.example/",
		"TikTok": "https://tiktok.example/", "Facebook": "https://facebook.example/",
	}
}

// payrollSweepData feeds the template sweeps every page branch: a normal net
// row with a visit and a refund line, a missing-rate row, a monthly-fixed row
// whose fixed the week view omits, and a paid badge.
func payrollSweepData(lang string) pageData {
	report, rows := payrollTestReport(service.PayrollKindWeek)
	report.Rows = nil // the page reads Rows; Report carries the totals
	// The paid snapshot differs from the recomputed row on purpose: the sweep
	// must render the snapshot-wins line (a refund landed after payment).
	rows[0].Paid.TotalIDR = 60_000
	rows[0].SnapshotDiff = true
	start := report.Period.Start.Format(payrollDayLayout)
	return pageData{
		"Lang":        lang,
		"CSRF":        "tok",
		"Success":     "pr.payok",
		"Error":       "pr.already",
		"Contact":     payrollContactData(),
		"Kind":        report.Period.Kind,
		"Start":       start,
		"PeriodLabel": payrollPeriodLabel(lang, report.Period),
		"PrevURL":     payrollURL(report.Period.Kind, report.Period.Start.AddDate(0, 0, -7)),
		"NextURL":     payrollURL(report.Period.Kind, report.Period.Start.AddDate(0, 0, 7)),
		"TodayURL":    payrollURL(report.Period.Kind, payrollToday()),
		"CSVURL":      "/admin/payroll.csv?kind=" + report.Period.Kind + "&start=" + start,
		"Report":      report,
		"Rows":        rows,
		"Missing":     []string{"Bima"},
		"Sessions":    4,
	}
}

// payrollEmptySweepData is the same period with no completed visits: the page
// must say so (pr.empty) instead of rendering an empty table head.
func payrollEmptySweepData(lang string) pageData {
	report, _ := payrollTestReport(service.PayrollKindWeek)
	report.Rows = nil
	report.MissingRate = 0
	report.TotalGrossIDR, report.TotalRefundIDR = 0, 0
	report.TotalCommissionIDR, report.TotalFixedIDR, report.TotalIDR = 0, 0, 0
	start := report.Period.Start.Format(payrollDayLayout)
	return pageData{
		"Lang":        lang,
		"Contact":     payrollContactData(),
		"Kind":        report.Period.Kind,
		"Start":       start,
		"PeriodLabel": payrollPeriodLabel(lang, report.Period),
		"PrevURL":     payrollURL(report.Period.Kind, report.Period.Start.AddDate(0, 0, -7)),
		"NextURL":     payrollURL(report.Period.Kind, report.Period.Start.AddDate(0, 0, 7)),
		"TodayURL":    payrollURL(report.Period.Kind, payrollToday()),
		"CSVURL":      "/admin/payroll.csv?kind=" + report.Period.Kind + "&start=" + start,
		"Report":      report,
		"Rows":        []payrollRowView{},
		"Missing":     []string{},
		"Sessions":    0,
	}
}

// Salaries are admin-only: a CS session is refused on both the page and the
// export, and an anonymous request is sent to login (never a payroll shell).
func TestAdminPayrollRequiresAdmin(t *testing.T) {
	report, _ := payrollTestReport(service.PayrollKindMonth)
	h := newPayrollServer(t, &fakePayrollReporter{report: report}, &fakePayoutStore{})

	for _, path := range []string{"/admin/payroll", "/admin/payroll.csv"} {
		if rec := asUser(h, path, domain.RoleCS); rec.Code != http.StatusForbidden {
			t.Errorf("cs %s status = %d, want 403", path, rec.Code)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusSeeOther {
			t.Errorf("anonymous %s status = %d, want 303", path, rec.Code)
		}
		if got, want := rec.Header().Get("Location"), "/login?next="+path; got != want {
			t.Errorf("anonymous %s Location = %q, want %q", path, got, want)
		}
	}
}

func TestAdminPayrollRenders(t *testing.T) {
	report, rows := payrollTestReport(service.PayrollKindWeek)
	payouts := &fakePayoutStore{byKey: map[string]*domain.StaffPayout{
		payrollPayoutKey(rows[0].StaffID, payrollPayoutKind(report.Period.Kind), report.Period.Start): rows[0].Paid,
	}}
	h := newPayrollServer(t, &fakePayrollReporter{report: report}, payouts)

	rec := adminGet(t, h, "/admin/payroll?kind=week&start=2026-11-25")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()
	for _, want := range []string{
		payrollPeriodLabel("id", report.Period), // period label
		"Ayu", "Bima", "Citra",                  // one row per therapist
		i18n.T("id", "pr.miss.w"),   // missing-rate banner
		i18n.T("id", "pr.fixed.mo"), // monthly fixed note on the week view
		i18n.T("id", "pr.paid"),     // paid badge
		i18n.T("id", "pr.unpaid"),   // unpaid label
		"no-print",                  // print chrome is excluded
	} {
		if !strings.Contains(out, want) {
			t.Errorf("payroll page missing %q", want)
		}
	}
	if strings.Contains(out, `"pr.`) {
		t.Errorf("payroll page leaks a raw dict key: %s", firstPayrollKeyLeak(out))
	}
}

func firstPayrollKeyLeak(out string) string {
	for _, f := range strings.Split(out, `"`) {
		if strings.HasPrefix(f, "pr.") {
			return f
		}
	}
	return ""
}

// Period navigation is pure arithmetic: a week steps ±7 days, a month steps to
// the first of the neighbouring month (day-1 normalisation), and an unnormalised
// start folds into its own period first.
func TestPayrollStep(t *testing.T) {
	wib := payrollTestWIB()
	day := func(d int) time.Time { return time.Date(2026, 11, d, 0, 0, 0, 0, wib) }
	month := func(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, wib) }

	cases := []struct {
		name       string
		kind       string
		start      time.Time
		wantPrev   time.Time
		wantNext   time.Time
		describeFn func(time.Time) string
	}{
		{"week monday", service.PayrollKindWeek, day(23), day(16), day(30), func(t time.Time) string { return t.Format(payrollDayLayout) }},
		{"week from a wednesday folds to monday", service.PayrollKindWeek, day(25), day(16), day(30), func(t time.Time) string { return t.Format(payrollDayLayout) }},
		{"month first", service.PayrollKindMonth, month(2026, 11, 1), month(2026, 10, 1), month(2026, 12, 1), func(t time.Time) string { return t.Format(payrollDayLayout) }},
		{"month mid folds to first", service.PayrollKindMonth, month(2026, 11, 25), month(2026, 10, 1), month(2026, 12, 1), func(t time.Time) string { return t.Format(payrollDayLayout) }},
		{"month across the year edge", service.PayrollKindMonth, month(2027, 1, 15), month(2026, 12, 1), month(2027, 2, 1), func(t time.Time) string { return t.Format(payrollDayLayout) }},
	}
	for _, tc := range cases {
		prev, next := payrollStep(tc.kind, tc.start)
		if got, want := tc.describeFn(prev), tc.describeFn(tc.wantPrev); got != want {
			t.Errorf("%s: prev = %s, want %s", tc.name, got, want)
		}
		if got, want := tc.describeFn(next), tc.describeFn(tc.wantNext); got != want {
			t.Errorf("%s: next = %s, want %s", tc.name, got, want)
		}
	}

	// "Today" is the current WIB calendar day, never the UTC one.
	today := payrollToday()
	now := time.Now().In(wib)
	if today.Format(payrollDayLayout) != now.Format(payrollDayLayout) {
		t.Errorf("payrollToday = %s, want WIB today %s", today.Format(payrollDayLayout), now.Format(payrollDayLayout))
	}
	if today.Hour() != 0 || today.Minute() != 0 {
		t.Errorf("payrollToday = %v, want midnight", today)
	}
}

func TestPayrollParams(t *testing.T) {
	wib := payrollTestWIB()
	cases := []struct {
		name      string
		query     string
		wantKind  string
		wantStart string
	}{
		{"default month", "", service.PayrollKindMonth, ""},
		{"week", "?kind=week", service.PayrollKindWeek, ""},
		{"unknown kind is month", "?kind=quarter", service.PayrollKindMonth, ""},
		{"explicit start", "?kind=week&start=2026-11-25", service.PayrollKindWeek, "2026-11-25"},
		{"malformed start falls back to today", "?start=2026-13-40", service.PayrollKindMonth, ""},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/admin/payroll"+tc.query, nil)
		kind, start := payrollParams(req)
		if kind != tc.wantKind {
			t.Errorf("%s: kind = %q, want %q", tc.name, kind, tc.wantKind)
		}
		if tc.wantStart != "" {
			if got := start.In(wib).Format(payrollDayLayout); got != tc.wantStart {
				t.Errorf("%s: start = %s, want %s", tc.name, got, tc.wantStart)
			}
		} else if start.Format(payrollDayLayout) != payrollToday().Format(payrollDayLayout) {
			t.Errorf("%s: start = %s, want today %s", tc.name, start.Format(payrollDayLayout), payrollToday().Format(payrollDayLayout))
		}
	}
}

func TestAdminPayrollCSV(t *testing.T) {
	report, rows := payrollTestReport(service.PayrollKindMonth)
	payouts := &fakePayoutStore{byKey: map[string]*domain.StaffPayout{
		payrollPayoutKey(rows[0].StaffID, payrollPayoutKind(report.Period.Kind), report.Period.Start): rows[0].Paid,
	}}
	h := newPayrollServer(t, &fakePayrollReporter{report: report}, payouts)

	rec := adminGet(t, h, "/admin/payroll.csv?kind=month&start=2026-11-01")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q, want text/csv", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "payroll-2026-11-01.csv") {
		t.Errorf("Content-Disposition = %q, want payroll-2026-11-01.csv", cd)
	}
	body := rec.Body.Bytes()
	if !bytes.HasPrefix(body, []byte("\xef\xbb\xbf")) {
		t.Errorf("CSV is missing the UTF-8 BOM")
	}
	records, err := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")))).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("csv is empty")
	}
	wantHead := []string{
		i18n.T("id", "pr.th.staff"), i18n.T("id", "pr.th.sess"),
		i18n.T("id", "pr.th.gross"), i18n.T("id", "pr.th.refund"),
		i18n.T("id", "pr.th.rate"), i18n.T("id", "pr.th.comm"),
		i18n.T("id", "pr.th.fixed"), i18n.T("id", "pr.th.total"),
		i18n.T("id", "pr.th.paid"),
	}
	if strings.Join(records[0], "|") != strings.Join(wantHead, "|") {
		t.Errorf("header = %v, want %v", records[0], wantHead)
	}
	// One row per therapist, in the report's order, plus the totals record.
	if len(records) < 1+len(rows)+1 {
		t.Fatalf("csv has %d records, want at least %d", len(records), 1+len(rows)+1)
	}
	for i, row := range rows {
		if got := records[1+i][0]; got != row.StaffName {
			t.Errorf("row %d therapist = %q, want %q", i, got, row.StaffName)
		}
	}
	totalRow := records[1+len(rows)]
	if totalRow[0] != i18n.T("id", "pr.total") {
		t.Errorf("totals row label = %q, want %q", totalRow[0], i18n.T("id", "pr.total"))
	}
	// The period label is part of the export, after the table.
	var labelRow []string
	for _, rec := range records {
		if len(rec) >= 2 && rec[0] == i18n.T("id", "pr.period") {
			labelRow = rec
		}
	}
	if labelRow == nil || labelRow[1] != payrollPeriodLabel("id", report.Period) {
		t.Errorf("period label row = %v, want %q", labelRow, payrollPeriodLabel("id", report.Period))
	}
}

// Salaries must never appear on a CS screen: the portal and its shared partials
// carry no payroll copy and no link into the admin payroll page. portal_nav is
// included on purpose — CS sees it, so it cannot advertise the payroll route.
func TestPayrollNotOnStaffFacingTemplates(t *testing.T) {
	for _, dir := range []string{"templates/portal", "templates/partials"} {
		err := fs.WalkDir(templateFS, dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".html") {
				return nil
			}
			b, err := templateFS.ReadFile(path)
			if err != nil {
				return err
			}
			src := string(b)
			if strings.Contains(src, `"pr.`) {
				t.Errorf("%s: payroll dict key on a staff-facing screen", path)
			}
			if strings.Contains(src, "/admin/payroll") {
				t.Errorf("%s: payroll link on a staff-facing screen", path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
}

// The router hands the service the whitelisted kind and the parsed start: an
// unknown kind falls back to the month view and a malformed start to today,
// rather than leaking the raw query into the report.
func TestAdminPayrollPassesPeriodToService(t *testing.T) {
	report, _ := payrollTestReport(service.PayrollKindWeek)
	rep := &fakePayrollReporter{report: report}
	h := newPayrollServer(t, rep, &fakePayoutStore{})

	if rec := adminGet(t, h, "/admin/payroll?kind=week&start=2026-11-25"); rec.Code != http.StatusOK {
		t.Fatalf("week status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if rep.calls != 1 {
		t.Fatalf("service calls = %d, want 1", rep.calls)
	}
	if rep.kind != service.PayrollKindWeek {
		t.Errorf("kind = %q, want %q", rep.kind, service.PayrollKindWeek)
	}
	if got := rep.start.In(payrollWIB).Format(payrollDayLayout); got != "2026-11-25" {
		t.Errorf("start = %s, want the requested 2026-11-25", got)
	}

	rep.calls = 0
	if rec := adminGet(t, h, "/admin/payroll?kind=quarter&start=2026-13-40"); rec.Code != http.StatusOK {
		t.Fatalf("fallback status = %d, want 200", rec.Code)
	}
	if rep.calls != 1 || rep.kind != service.PayrollKindMonth {
		t.Errorf("fallback kind = %q (%d calls), want month", rep.kind, rep.calls)
	}
	if got, want := rep.start.In(payrollWIB).Format(payrollDayLayout), payrollToday().Format(payrollDayLayout); got != want {
		t.Errorf("fallback start = %s, want today %s", got, want)
	}
}

// An empty period still renders the empty-state copy, in both languages: the
// table head alone would read as a broken page.
func TestAdminPayrollEmptyPeriod(t *testing.T) {
	s, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	for _, lang := range []string{"en", "id"} {
		var buf bytes.Buffer
		if err := s.tpl.ExecuteTemplate(&buf, "admin_payroll.html", payrollEmptySweepData(lang)); err != nil {
			t.Fatalf("%s execute: %v", lang, err)
		}
		out := buf.String()
		if !strings.Contains(out, i18n.T(lang, "pr.empty")) {
			t.Errorf("%s: empty period is missing the pr.empty copy", lang)
		}
		if strings.Contains(out, ">pr.") {
			t.Errorf("%s: empty period leaks a raw dict key", lang)
		}
	}
}

// A missing-rate row with no staff name (a deleted or nameless therapist)
// prints the same "-" fallback the staff cell uses, never an empty <li>.
func TestAdminPayrollMissingNameFallback(t *testing.T) {
	s, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	data := payrollSweepData("id")
	data["Missing"] = []string{""}
	var buf bytes.Buffer
	if err := s.tpl.ExecuteTemplate(&buf, "admin_payroll.html", data); err != nil {
		t.Fatalf("execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "<li>-</li>") {
		t.Error("a nameless missing-rate row must render the '-' fallback")
	}
	if strings.Contains(out, "<li></li>") {
		t.Error("a nameless missing-rate row rendered an empty <li>")
	}
}

// failingWriter is a client that vanished mid-download: every write fails, so
// the export is truncated and the handler must say so in the log.
type failingWriter struct{ header http.Header }

func (f *failingWriter) Header() http.Header       { return f.header }
func (f *failingWriter) Write([]byte) (int, error) { return 0, errors.New("client gone") }
func (f *failingWriter) WriteHeader(_ int)         {}

// A truncated CSV must leave a server log line; a discarded error would make a
// half-written payroll export look like a successful one.
func TestAdminPayrollCSVLogsWriteErrors(t *testing.T) {
	report, _ := payrollTestReport(service.PayrollKindMonth)
	logs := &bytes.Buffer{}
	s, err := New(config.Config{}, slog.New(slog.NewTextHandler(logs, nil)), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	s.payroll = &fakePayrollReporter{report: report}
	s.payouts = &fakePayoutStore{}

	req := httptest.NewRequest(http.MethodGet, "/admin/payroll.csv?kind=month&start=2026-11-01", nil)
	s.AdminPayrollCSV(&failingWriter{header: http.Header{}}, req)

	if out := logs.String(); !strings.Contains(out, "admin payroll csv") {
		t.Errorf("a failed CSV write was not logged: %q", out)
	}
}
