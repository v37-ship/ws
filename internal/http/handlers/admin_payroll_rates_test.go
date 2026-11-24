package handlers

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"spa-booking/internal/config"
	"spa-booking/internal/domain"
	"spa-booking/internal/http/middleware"
	"spa-booking/internal/i18n"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Rate management is the admin form over the append-only staff_comp timeline:
// it parses a percent in either locale (12,5 or 12.5), rejects a duplicate
// effective date instead of relying on the created_at tie-break, and never
// edits a row. The reads and the write are repo calls, so the tests swap them
// through the seams and drive the real router.

var (
	rateAyuID  = uuid.MustParse("aaaaaaaa-1111-1111-1111-111111111111")
	rateBimaID = uuid.MustParse("bbbbbbbb-2222-2222-2222-222222222222")
	rateAdmID  = uuid.MustParse("cccccccc-3333-3333-3333-333333333333")
)

// rateAdmin is the authenticated admin the form post records as CreatedBy.
func rateAdmin() *domain.User {
	return &domain.User{ID: rateAdmID, Role: domain.RoleAdmin, FullName: "Rina Admin"}
}

// rateTestStaff mirrors the repo read: every staff member, ordered by name,
// both actives and one inactive (a rate still applies to a former therapist's
// past periods).
func rateTestStaff() []domain.Staff {
	return []domain.Staff{
		{ID: rateAyuID, Name: "Ayu", Active: true},
		{ID: rateBimaID, Name: "Bima", Active: false},
	}
}

// rateTestRows is Ayu's timeline: an old row, the current one, and a future
// raise queued ahead of today. The future row must win nothing yet, but it is
// the newest row in the history list.
func rateTestRows() []domain.StaffComp {
	today := payrollToday()
	created := time.Date(2026, 11, 20, 9, 0, 0, 0, payrollWIB)
	creator := rateAdmID
	return []domain.StaffComp{
		{
			ID:      uuid.MustParse("dddddddd-0000-0000-0000-000000000001"),
			StaffID: rateAyuID, PercentBp: 1250, FixedIDR: 150_000,
			Cadence: domain.CompCadenceMonthly, CommissionBase: domain.CompBaseNet,
			EffectiveFrom: today.AddDate(-1, 0, 0), CreatedBy: &creator, CreatedAt: created,
		},
		{
			ID:      uuid.MustParse("dddddddd-0000-0000-0000-000000000002"),
			StaffID: rateAyuID, PercentBp: 1000, FixedIDR: 50_000,
			Cadence: domain.CompCadenceWeekly, CommissionBase: domain.CompBaseList,
			EffectiveFrom: today.AddDate(-2, 0, 0), CreatedAt: created,
		},
		{
			ID:      uuid.MustParse("dddddddd-0000-0000-0000-000000000003"),
			StaffID: rateAyuID, PercentBp: 1500, FixedIDR: 200_000,
			Cadence: domain.CompCadenceMonthly, CommissionBase: domain.CompBaseNet,
			EffectiveFrom: today.AddDate(0, 1, 0), CreatedAt: created,
		},
	}
}

// ratesSuite is one wired server plus the fakes behind its seams.
type ratesSuite struct {
	handler   http.Handler
	staff     []domain.Staff
	rates     []domain.StaffComp
	names     map[uuid.UUID]string
	staffErr  error
	ratesErr  error
	createErr error
	created   []domain.StaffComp
	logs      *bytes.Buffer
}

// newRatesSuite swaps the four rate-page seams for the test's fakes and builds
// the real router. Swapping is process-wide, so the cleanup restores them.
func newRatesSuite(t *testing.T) *ratesSuite {
	t.Helper()
	suite := &ratesSuite{
		staff: rateTestStaff(),
		rates: rateTestRows(),
		names: map[uuid.UUID]string{rateAdmID: "Rina Admin"},
		logs:  &bytes.Buffer{},
	}
	prevStaff, prevHistory, prevCreate, prevName := payrollStaffList, payrollRateHistory, payrollRateCreate, payrollUserName
	t.Cleanup(func() {
		payrollStaffList, payrollRateHistory, payrollRateCreate, payrollUserName = prevStaff, prevHistory, prevCreate, prevName
	})
	payrollStaffList = func(_ *Server, _ *http.Request) ([]domain.Staff, error) {
		return suite.staff, suite.staffErr
	}
	payrollRateHistory = func(_ *Server, _ *http.Request) ([]domain.StaffComp, error) {
		return suite.rates, suite.ratesErr
	}
	payrollRateCreate = func(_ *Server, _ *http.Request, c *domain.StaffComp) error {
		if suite.createErr != nil {
			return suite.createErr
		}
		suite.created = append(suite.created, *c)
		return nil
	}
	payrollUserName = func(_ *Server, _ *http.Request, id uuid.UUID) string {
		return suite.names[id]
	}

	s, err := New(config.Config{}, slog.New(slog.NewTextHandler(suite.logs, nil)), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	r := chi.NewRouter()
	s.Routes(r)
	suite.handler = r
	return suite
}

// ratesPost drives one form post the way the browser does: the CSRF cookie and
// the csrf form field hold the same token, and LoadUser has already resolved
// the identity.
func ratesPost(t *testing.T, h http.Handler, u *domain.User, form url.Values, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	form.Set("csrf", "tok")
	req := httptest.NewRequest(http.MethodPost, "/admin/payroll/rates", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	req.AddCookie(&http.Cookie{Name: "csrf", Value: "tok"})
	if u != nil {
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserKey, u))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// rateValidForm is a complete submission: the staff id, a comma decimal, a
// whole-rupiah fixed, and the two enums.
func rateValidForm() url.Values {
	return url.Values{
		"staff_id":  {rateAyuID.String()},
		"percent":   {"12,5"},
		"fixed":     {"150000"},
		"cadence":   {domain.CompCadenceMonthly},
		"base":      {domain.CompBaseNet},
		"effective": {"2026-12-01"},
	}
}

// A percent is entered in either locale and stored in basis points; anything
// beyond two decimals, out of range, or non-numeric is refused rather than
// rounded silently.
func TestParsePercentBp(t *testing.T) {
	cases := []struct {
		in   string
		want int
		ok   bool
	}{
		{"25", 2500, true},
		{"25.5", 2550, true},
		{"25,5", 2550, true},
		{"0", 0, true},
		{"100", 10000, true},
		{"0.05", 5, true},
		{" 12.5 ", 1250, true},
		{".5", 50, true},
		{"100.01", 0, false},
		{"-1", 0, false},
		{"abc", 0, false},
		{"1.234", 0, false},
		{"12.", 0, false},
		{"", 0, false},
		{"12,5,5", 0, false},
		{"1e2", 0, false},
	}
	for _, tc := range cases {
		got, ok := parsePercentBp(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("parsePercentBp(%q) = (%d, %v), want (%d, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// Fixed IDR is whole rupiah: a thousands separator the operator typed is a
// typo, not a number, and an empty field means zero.
func TestParseFixedIDR(t *testing.T) {
	cases := []struct {
		in   string
		want int64
		ok   bool
	}{
		{"0", 0, true},
		{"", 0, true},
		{"150000", 150_000, true},
		{" 2500 ", 2500, true},
		{"-5", 0, false},
		{"1,000", 0, false},
		{"12.5", 0, false},
		{"abc", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseFixedIDR(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("parseFixedIDR(%q) = (%d, %v), want (%d, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// buildStaffRate is the whole form contract in one place: staff selection,
// percent, fixed, the two enums, the date, and the duplicate-date refusal.
func TestBuildStaffRate(t *testing.T) {
	existing := rateTestRows()
	staff := rateTestStaff()

	cases := []struct {
		name    string
		mutate  func(url.Values)
		wantErr string
	}{
		{"valid", func(url.Values) {}, ""},
		{"percent at the ceiling", func(f url.Values) { f.Set("percent", "100") }, ""},
		{"percent over the ceiling", func(f url.Values) { f.Set("percent", "100.01") }, "pr.pct.bad"},
		{"percent not numeric", func(f url.Values) { f.Set("percent", "abc") }, "pr.pct.bad"},
		{"percent empty", func(f url.Values) { f.Set("percent", "") }, "pr.pct.bad"},
		{"percent three decimals", func(f url.Values) { f.Set("percent", "1.234") }, "pr.pct.bad"},
		{"percent negative", func(f url.Values) { f.Set("percent", "-1") }, "pr.pct.bad"},
		{"fixed negative", func(f url.Values) { f.Set("fixed", "-5") }, "pr.fixed.bad"},
		{"fixed with a separator", func(f url.Values) { f.Set("fixed", "1,000") }, "pr.fixed.bad"},
		{"fixed empty is zero", func(f url.Values) { f.Set("fixed", "") }, ""},
		{"cadence missing", func(f url.Values) { f.Set("cadence", "") }, "pr.cad.bad"},
		{"cadence unknown", func(f url.Values) { f.Set("cadence", "yearly") }, "pr.cad.bad"},
		{"base unknown", func(f url.Values) { f.Set("base", "gross") }, "pr.base.bad"},
		{"base missing", func(f url.Values) { f.Set("base", "") }, "pr.base.bad"},
		{"date impossible", func(f url.Values) { f.Set("effective", "2026-02-30") }, "pr.eff.bad"},
		{"date empty", func(f url.Values) { f.Set("effective", "") }, "pr.eff.bad"},
		{"date not ISO", func(f url.Values) { f.Set("effective", "01/12/2026") }, "pr.eff.bad"},
		{"staff missing", func(f url.Values) { f.Set("staff_id", "") }, "pr.staff.bad"},
		{"staff unknown", func(f url.Values) { f.Set("staff_id", uuid.New().String()) }, "pr.staff.bad"},
		{"staff not a uuid", func(f url.Values) { f.Set("staff_id", "ayu") }, "pr.staff.bad"},
		{"duplicate effective date", func(f url.Values) {
			f.Set("effective", payrollDateKey(existing[0].EffectiveFrom))
		}, "pr.dup"},
		{"same date for another therapist is allowed", func(f url.Values) {
			f.Set("staff_id", rateBimaID.String())
			f.Set("effective", payrollDateKey(existing[0].EffectiveFrom))
		}, ""},
		{"inactive staff may still get a rate", func(f url.Values) { f.Set("staff_id", rateBimaID.String()) }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			form := rateValidForm()
			tc.mutate(form)
			comp, key := buildStaffRate(rateForm{
				StaffID:   form.Get("staff_id"),
				Percent:   form.Get("percent"),
				Fixed:     form.Get("fixed"),
				Cadence:   form.Get("cadence"),
				Base:      form.Get("base"),
				Effective: form.Get("effective"),
			}, staff, existing)
			if key != tc.wantErr {
				t.Fatalf("buildStaffRate error = %q, want %q", key, tc.wantErr)
			}
			if tc.wantErr != "" {
				if comp != nil {
					t.Fatalf("buildStaffRate returned %+v alongside error %q", comp, key)
				}
				return
			}
			if comp == nil {
				t.Fatal("buildStaffRate returned a nil comp")
			}
			if comp.StaffID == uuid.Nil || payrollDateKey(comp.EffectiveFrom) != form.Get("effective") {
				t.Fatalf("comp staff/effective = %v/%s, want %s/%s",
					comp.StaffID, payrollDateKey(comp.EffectiveFrom), form.Get("staff_id"), form.Get("effective"))
			}
			if !domain.ValidPercentBp(comp.PercentBp) || !domain.ValidCadence(comp.Cadence) ||
				!domain.ValidCommissionBase(comp.CommissionBase) || comp.FixedIDR < 0 {
				t.Fatalf("comp is not form-valid: %+v", comp)
			}
		})
	}

	// The comma and dot spellings are the same rate.
	dot := rateValidForm()
	dot.Set("percent", "12.5")
	a, _ := buildStaffRate(rateForm{StaffID: dot.Get("staff_id"), Percent: dot.Get("percent"),
		Fixed: dot.Get("fixed"), Cadence: dot.Get("cadence"), Base: dot.Get("base"), Effective: dot.Get("effective")}, staff, existing)
	b, _ := buildStaffRate(rateForm{StaffID: rateAyuID.String(), Percent: "12,5",
		Fixed: "150000", Cadence: "monthly", Base: "net", Effective: "2026-12-01"}, staff, existing)
	if a == nil || b == nil || a.PercentBp != 1250 || b.PercentBp != 1250 {
		t.Fatalf("locale spellings diverged: %+v vs %+v", a, b)
	}
}

// Salaries are admin-only on the rate page too: CS is refused on the read and
// the write, and an anonymous request is sent to login.
func TestAdminPayrollRatesRequiresAdmin(t *testing.T) {
	suite := newRatesSuite(t)

	if rec := asUser(suite.handler, "/admin/payroll/rates", domain.RoleCS); rec.Code != http.StatusForbidden {
		t.Errorf("cs GET status = %d, want 403", rec.Code)
	}
	if rec := ratesPost(t, suite.handler, &domain.User{Role: domain.RoleCS}, rateValidForm(), false); rec.Code != http.StatusForbidden {
		t.Errorf("cs POST status = %d, want 403", rec.Code)
	}
	rec := httptest.NewRecorder()
	suite.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/admin/payroll/rates", nil))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login?next=/admin/payroll/rates" {
		t.Errorf("anonymous GET = %d %q, want 303 to login", rec.Code, rec.Header().Get("Location"))
	}
	if rec := ratesPost(t, suite.handler, nil, rateValidForm(), false); rec.Code != http.StatusSeeOther {
		t.Errorf("anonymous POST = %d, want 303", rec.Code)
	}
	if len(suite.created) != 0 {
		t.Errorf("a refused request still created %d rows", len(suite.created))
	}
}

// The page shows the form defaults, every therapist with the inactive marker,
// the rate in force today, and the full timeline newest first.
func TestAdminPayrollRatesRenders(t *testing.T) {
	suite := newRatesSuite(t)
	rec := adminGet(t, suite.handler, "/admin/payroll/rates")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()
	rows := rateTestRows()

	for _, want := range []string{
		`href="/admin/payroll"`,    // the way back to the period page
		i18n.T("id", "pr.rates"),   // page title
		i18n.T("id", "pr.newrate"), // the form heading
		i18n.T("id", "pr.pct"),     // form labels
		i18n.T("id", "pr.eff"),
		i18n.T("id", "pr.save"),
		`value="` + payrollToday().Format(payrollDayLayout) + `"`, // effective default is WIB today
		"<option value=\"" + rateAyuID.String() + "\"",
		"<option value=\"" + rateBimaID.String() + "\"",
		i18n.T("id", "pr.inact"),  // Bima is inactive
		ratePercent(1250),         // current rate for Ayu
		"Rp 150.000",              // its fixed component
		i18n.T("id", "pr.norate"), // Bima has no row yet
		i18n.T("id", "pr.cur"),    // current-rate label
		i18n.T("id", "pr.hist"),   // history heading (or table caption)
		"Rina Admin",              // the creator's name, not a raw uuid
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rate page missing %q", want)
		}
	}

	// History is newest first: the future raise, the current row, the old one.
	last := -1
	for _, row := range []domain.StaffComp{rows[2], rows[0], rows[1]} {
		// The history cell, not the current-rate line above it, is what orders.
		pos := strings.Index(out, ">"+i18n.DateLong("id", row.EffectiveFrom.In(payrollWIB))+"</td>")
		if pos < 0 {
			t.Fatalf("history date %s is not rendered", payrollDateKey(row.EffectiveFrom))
		}
		if pos < last {
			t.Fatalf("history is not newest-first: %s appears before the row above it", payrollDateKey(row.EffectiveFrom))
		}
		last = pos
	}

	if strings.Contains(out, `"pr.`) || strings.Contains(out, ">pr.") {
		t.Errorf("rate page leaks a raw dict key: %s", firstPayrollKeyLeak(out))
	}
}

// A successful post redirects (the house pattern) with the success flag the
// page translates, and records the operator as CreatedBy.
func TestAdminPayrollRatesCreate(t *testing.T) {
	suite := newRatesSuite(t)
	rec := ratesPost(t, suite.handler, rateAdmin(), rateValidForm(), false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Header().Get("Location"), "/admin/payroll/rates?ok=1"; got != want {
		t.Errorf("Location = %q, want %q", got, want)
	}
	if len(suite.created) != 1 {
		t.Fatalf("created %d rows, want 1", len(suite.created))
	}
	got := suite.created[0]
	if got.StaffID != rateAyuID || got.PercentBp != 1250 || got.FixedIDR != 150_000 ||
		got.Cadence != domain.CompCadenceMonthly || got.CommissionBase != domain.CompBaseNet {
		t.Fatalf("created row drifted: %+v", got)
	}
	if payrollDateKey(got.EffectiveFrom) != "2026-12-01" {
		t.Errorf("effective = %s, want 2026-12-01", payrollDateKey(got.EffectiveFrom))
	}
	if got.CreatedBy == nil || *got.CreatedBy != rateAdmID {
		t.Errorf("CreatedBy = %v, want %v", got.CreatedBy, rateAdmID)
	}

	// The redirect target renders the success banner in the page's language.
	rec = adminGet(t, suite.handler, "/admin/payroll/rates?ok=1")
	if !strings.Contains(rec.Body.String(), i18n.T("id", "pr.rateok")) {
		t.Error("success flag does not render the success copy")
	}
}

// A validation failure re-renders the page at 200 with the message and the
// submitted values, so the operator never loses what they typed.
func TestAdminPayrollRatesValidationInline(t *testing.T) {
	suite := newRatesSuite(t)
	form := rateValidForm()
	form.Set("percent", "abc")
	form.Set("fixed", "99000")
	rec := ratesPost(t, suite.handler, rateAdmin(), form, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (inline error); body: %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()
	if !strings.Contains(out, i18n.T("id", "pr.pct.bad")) {
		t.Errorf("page is missing the percent error copy")
	}
	if !strings.Contains(out, `value="abc"`) || !strings.Contains(out, `value="99000"`) {
		t.Errorf("submitted values were not preserved")
	}
	if !strings.Contains(out, `<option value="`+rateAyuID.String()+`" selected`) {
		t.Errorf("submitted therapist was not preserved")
	}
	if len(suite.created) != 0 {
		t.Errorf("an invalid post created %d rows", len(suite.created))
	}

	// A duplicate effective date is the same inline contract.
	dup := rateValidForm()
	dup.Set("effective", payrollDateKey(rateTestRows()[0].EffectiveFrom))
	rec = ratesPost(t, suite.handler, rateAdmin(), dup, false)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), i18n.T("id", "pr.dup")) {
		t.Errorf("duplicate date = %d, missing the duplicate copy", rec.Code)
	}
	if len(suite.created) != 0 {
		t.Errorf("a duplicate post created %d rows", len(suite.created))
	}
}

// A failed write is not a validation error: it takes the staffFail contract so
// the button is never silently dead.
func TestAdminPayrollRatesCreateHTMXFailure(t *testing.T) {
	suite := newRatesSuite(t)
	suite.createErr = errTestRateCreate
	rec := ratesPost(t, suite.handler, rateAdmin(), rateValidForm(), true)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := rec.Header().Get("HX-Reswap"); got != "none" {
		t.Errorf("HX-Reswap = %q, want none", got)
	}
	if !strings.Contains(rec.Header().Get("HX-Trigger"), "toast") {
		t.Errorf("HX-Trigger = %q, want a toast", rec.Header().Get("HX-Trigger"))
	}
	if !strings.Contains(suite.logs.String(), "payroll rate") {
		t.Errorf("the write failure was not logged: %q", suite.logs.String())
	}
}

// A stale CSRF token on the rate form is the same dead-button trap as every
// other staff POST: HTMX gets the real status plus a toast, a plain form gets
// the portal error page.
func TestAdminPayrollRatesCSRF(t *testing.T) {
	suite := newRatesSuite(t)
	form := url.Values{"staff_id": {rateAyuID.String()}}
	req := httptest.NewRequest(http.MethodPost, "/admin/payroll/rates", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserKey, rateAdmin()))
	rec := httptest.NewRecorder()
	suite.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("plain CSRF failure status = %d, want 200 (portal error page)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "portal-error") {
		t.Errorf("plain CSRF failure did not render the portal error page")
	}
	if len(suite.created) != 0 {
		t.Errorf("a CSRF-less post created %d rows", len(suite.created))
	}
}

// Read failures are an outage, not an empty page: the admin gets an honest 500.
func TestAdminPayrollRatesReadError(t *testing.T) {
	suite := newRatesSuite(t)
	suite.ratesErr = errTestRateRead
	rec := adminGet(t, suite.handler, "/admin/payroll/rates")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// The same timeline renders in both languages: localized dates, the same
// numbers, and the missing-rate copy.
func TestAdminPayrollRatesHistoryBothLanguages(t *testing.T) {
	suite := newRatesSuite(t)
	rows := rateTestRows()
	for _, lang := range []string{"en", "id"} {
		rec := adminGet(t, suite.handler, "/admin/payroll/rates?lang="+lang)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", lang, rec.Code)
		}
		out := rec.Body.String()
		last := -1
		for _, row := range []domain.StaffComp{rows[2], rows[0], rows[1]} {
			want := i18n.DateLong(lang, row.EffectiveFrom.In(payrollWIB))
			pos := strings.Index(out, ">"+want+"</td>")
			if pos < 0 {
				t.Fatalf("%s: history date %q is not rendered", lang, want)
			}
			if pos < last {
				t.Fatalf("%s: history is not newest-first at %q", lang, want)
			}
			last = pos
		}
		for _, want := range []string{
			ratePercent(1250), i18n.T(lang, "pr.norate"),
			i18n.T(lang, "pr.weekly"), i18n.T(lang, "pr.base.list"),
		} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: rate page missing %q", lang, want)
			}
		}
		if strings.Contains(out, ">pr.") {
			t.Errorf("%s: rate page leaks a raw dict key", lang)
		}
	}
}

// ratePercent reverses the stored basis points without losing a hundredth:
// 1250 -> 12.5%, 1255 -> 12.55%, 2500 -> 25%.
func TestRatePercent(t *testing.T) {
	cases := map[int]string{0: "0%", 5: "0.05%", 1250: "12.5%", 1255: "12.55%", 2500: "25%", 10000: "100%"}
	for bp, want := range cases {
		if got := ratePercent(bp); got != want {
			t.Errorf("ratePercent(%d) = %q, want %q", bp, got, want)
		}
	}
}

var (
	errTestRateCreate = testError("rate create failed")
	errTestRateRead   = testError("rate read failed")
)

type testError string

func (e testError) Error() string { return string(e) }

// payrollRatesSweepData feeds the template sweeps every branch of the rate
// page: a therapist with a current rate and a full timeline (a future raise and
// a row with no recorded creator), a therapist with no rate at all, and an
// inactive one.
func payrollRatesSweepData(lang string) pageData {
	staff := append(rateTestStaff(), domain.Staff{
		ID:     uuid.MustParse("eeeeeeee-4444-4444-4444-444444444444"),
		Name:   "Citra",
		Active: true,
	})
	views := payrollRateStaffViews(staff, rateTestRows(), payrollToday(), map[uuid.UUID]string{rateAdmID: "Rina Admin"})
	return pageData{
		"Lang":    lang,
		"Staff":   views,
		"Form":    rateDefaults(payrollToday()),
		"Today":   payrollToday().Format(payrollDayLayout),
		"Success": "pr.rateok",
		"Error":   "pr.pct.bad",
	}
}
