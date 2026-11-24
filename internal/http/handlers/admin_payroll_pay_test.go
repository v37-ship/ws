package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/http/middleware"
	"spa-booking/internal/i18n"
	"spa-booking/internal/service"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Mark-paid is the one write on the payroll page. The POST recomputes the
// period and stores the resulting row as an immutable snapshot: posted amounts
// are never trusted, a missing rate is refused, and the unique key is what
// makes the second submit a plain "already paid" message instead of a 500.

var payrollPayAdminID = uuid.MustParse("dddddddd-5555-5555-5555-555555555555")

func payrollPayAdmin() *domain.User {
	return &domain.User{ID: payrollPayAdminID, Role: domain.RoleAdmin, FullName: "Rina Admin"}
}

func payrollPayStaffID(t *testing.T) uuid.UUID {
	t.Helper()
	_, rows := payrollTestReport(service.PayrollKindWeek)
	return rows[0].StaffID // Ayu: a normal rate row with a visit
}

func payrollPayForm(t *testing.T) url.Values {
	t.Helper()
	return url.Values{
		"staff": {payrollPayStaffID(t).String()},
		"kind":  {service.PayrollKindWeek},
		"start": {"2026-11-25"}, // a Wednesday: the handler normalises to Monday 23
		"note":  {"transfer BCA"},
	}
}

// payrollPayPost drives one form post the way the browser does: the CSRF
// cookie and field agree, and LoadUser has already resolved the identity.
func payrollPayPost(t *testing.T, h http.Handler, u *domain.User, form url.Values, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	form.Set("csrf", "tok")
	req := httptest.NewRequest(http.MethodPost, "/admin/payroll/pay", strings.NewReader(form.Encode()))
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

// payrollVoidPost drives the void form the way the browser does, with the same
// CSRF and identity plumbing as the pay form.
func payrollVoidPost(t *testing.T, h http.Handler, u *domain.User, form url.Values, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	form.Set("csrf", "tok")
	req := httptest.NewRequest(http.MethodPost, "/admin/payroll/payout/void", strings.NewReader(form.Encode()))
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

func payrollVoidForm(t *testing.T) url.Values {
	t.Helper()
	return url.Values{
		"id":     {uuid.New().String()},
		"kind":   {service.PayrollKindWeek},
		"start":  {"2026-11-25"},
		"reason": {"wrong amount"},
	}
}

// Void is admin-only like the rest of payroll, and a stale CSRF token keeps the
// house dead-button contract.
func TestAdminPayrollVoidRequiresAdminAndCSRF(t *testing.T) {
	report, _ := payrollTestReport(service.PayrollKindWeek)
	store := &fakePayoutStore{voidOK: true}
	h := newPayrollServer(t, &fakePayrollReporter{report: report}, store)

	if rec := payrollVoidPost(t, h, &domain.User{Role: domain.RoleCS}, payrollVoidForm(t), false); rec.Code != http.StatusForbidden {
		t.Errorf("cs POST status = %d, want 403", rec.Code)
	}
	rec := payrollVoidPost(t, h, nil, payrollVoidForm(t), false)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("anonymous POST status = %d, want 303", rec.Code)
	}

	form := payrollVoidForm(t)
	req := httptest.NewRequest(http.MethodPost, "/admin/payroll/payout/void", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserKey, payrollPayAdmin()))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "portal-error") {
		t.Errorf("plain CSRF failure = %d, want the portal error page at 200", rec.Code)
	}
	if len(store.voidCalls) != 0 {
		t.Errorf("a refused void reached the repo %d times", len(store.voidCalls))
	}
}

// A void needs a real reason: empty, over-long, and a malformed id all answer
// the same inline message and never reach the repo.
func TestAdminPayrollVoidRequiresReason(t *testing.T) {
	report, _ := payrollTestReport(service.PayrollKindWeek)
	for _, tc := range []struct{ name, id, reason string }{
		{"empty reason", uuid.New().String(), "   "},
		{"501-rune reason", uuid.New().String(), strings.Repeat("a", 501)},
		{"malformed id", "not-a-uuid", "wrong amount"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakePayoutStore{voidOK: true}
			h := newPayrollServer(t, &fakePayrollReporter{report: report}, store)
			form := payrollVoidForm(t)
			form.Set("id", tc.id)
			form.Set("reason", tc.reason)
			rec := payrollVoidPost(t, h, payrollPayAdmin(), form, false)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 inline", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), i18n.T("id", "pr.voidbad")) {
				t.Error("the void-reason refusal copy is missing")
			}
			if len(store.voidCalls) != 0 {
				t.Errorf("an invalid void reached the repo %d times", len(store.voidCalls))
			}
		})
	}
}

// A successful void re-renders the period with the success copy; an already
// voided row answers the already-voided message.
func TestAdminPayrollVoidSuccessAndAlreadyVoided(t *testing.T) {
	report, _ := payrollTestReport(service.PayrollKindWeek)

	t.Run("success", func(t *testing.T) {
		store := &fakePayoutStore{voidOK: true}
		h := newPayrollServer(t, &fakePayrollReporter{report: report}, store)
		form := payrollVoidForm(t)
		id := uuid.MustParse(form.Get("id"))
		rec := payrollVoidPost(t, h, payrollPayAdmin(), form, false)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), i18n.T("id", "pr.voidok")) {
			t.Error("the void success copy is missing")
		}
		if len(store.voidCalls) != 1 || store.voidCalls[0] != id {
			t.Errorf("void calls = %v, want [%v]", store.voidCalls, id)
		}
	})

	t.Run("already voided", func(t *testing.T) {
		store := &fakePayoutStore{voidOK: false}
		h := newPayrollServer(t, &fakePayrollReporter{report: report}, store)
		rec := payrollVoidPost(t, h, payrollPayAdmin(), payrollVoidForm(t), false)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 inline", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), i18n.T("id", "pr.voidgone")) {
			t.Error("the already-voided copy is missing")
		}
	})
}

// A void write failure is a real fault: the staffFail contract keeps the button
// honest and logs the cause.
func TestAdminPayrollVoidWriteFailure(t *testing.T) {
	report, _ := payrollTestReport(service.PayrollKindWeek)
	store := &fakePayoutStore{voidErr: testError("void failed")}
	h := newPayrollServer(t, &fakePayrollReporter{report: report}, store)

	rec := payrollVoidPost(t, h, payrollPayAdmin(), payrollVoidForm(t), true)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := rec.Header().Get("HX-Reswap"); got != "none" {
		t.Errorf("HX-Reswap = %q, want none", got)
	}
	if !strings.Contains(store.logs.String(), "admin payroll void") {
		t.Errorf("the void failure was not logged: %q", store.logs.String())
	}
}

// Salaries stay admin-only on the write too: CS is refused by the route group
// and an anonymous post is sent to login before the handler runs.
func TestAdminPayrollPayRequiresAdmin(t *testing.T) {
	report, _ := payrollTestReport(service.PayrollKindWeek)
	store := &fakePayoutStore{}
	h := newPayrollServer(t, &fakePayrollReporter{report: report}, store)

	if rec := payrollPayPost(t, h, &domain.User{Role: domain.RoleCS}, payrollPayForm(t), false); rec.Code != http.StatusForbidden {
		t.Errorf("cs POST status = %d, want 403", rec.Code)
	}
	rec := payrollPayPost(t, h, nil, payrollPayForm(t), false)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("anonymous POST status = %d, want 303", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/login?next=/admin/payroll/pay" {
		t.Errorf("anonymous Location = %q, want login with next", got)
	}
	if len(store.created) != 0 {
		t.Errorf("a refused post created %d rows", len(store.created))
	}
}

// A stale CSRF token is the house dead-button contract: HTMX gets the real
// status plus a toast, a plain form gets the portal error page, and nothing is
// written.
func TestAdminPayrollPayCSRF(t *testing.T) {
	report, _ := payrollTestReport(service.PayrollKindWeek)
	store := &fakePayoutStore{}
	h := newPayrollServer(t, &fakePayrollReporter{report: report}, store)

	form := url.Values{"staff": {payrollPayStaffID(t).String()}, "kind": {"week"}, "start": {"2026-11-25"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/payroll/pay", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserKey, payrollPayAdmin()))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "portal-error") {
		t.Errorf("plain CSRF failure = %d, want the portal error page at 200", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/admin/payroll/pay", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserKey, payrollPayAdmin()))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("htmx CSRF failure = %d, want 400", rec.Code)
	}
	if got := rec.Header().Get("HX-Reswap"); got != "none" {
		t.Errorf("HX-Reswap = %q, want none", got)
	}
	if !strings.Contains(rec.Header().Get("HX-Trigger"), "toast") {
		t.Errorf("HX-Trigger = %q, want a toast", rec.Header().Get("HX-Trigger"))
	}
	if len(store.created) != 0 {
		t.Errorf("a CSRF-less post created %d rows", len(store.created))
	}
}

// A successful post recomputes the period, ignores any posted amount, snapshots
// the computed row, records the operator, and redirects back to the same
// period with the success flag.
func TestAdminPayrollPaySuccess(t *testing.T) {
	report, _ := payrollTestReport(service.PayrollKindWeek)
	store := &fakePayoutStore{}
	h := newPayrollServer(t, &fakePayrollReporter{report: report}, store)

	form := payrollPayForm(t)
	form.Set("total", "1") // never trusted: the amount comes from the report
	form.Set("net_idr", "999999")
	rec := payrollPayPost(t, h, payrollPayAdmin(), form, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}
	wantLoc := "/admin/payroll?kind=week&start=2026-11-23&ok=1"
	if got := rec.Header().Get("Location"); got != wantLoc {
		t.Errorf("Location = %q, want %q", got, wantLoc)
	}
	if len(store.created) != 1 {
		t.Fatalf("created %d rows, want 1", len(store.created))
	}
	got := store.created[0]
	row := report.Rows[0]
	if got.StaffID != row.StaffID || got.PeriodKind != domain.PeriodWeekly {
		t.Errorf("staff/kind = %v/%q, want %v/%q", got.StaffID, got.PeriodKind, row.StaffID, domain.PeriodWeekly)
	}
	if got.PeriodStart.In(payrollWIB).Format(payrollDayLayout) != "2026-11-23" {
		t.Errorf("period start = %s, want the normalised 2026-11-23", got.PeriodStart.Format(payrollDayLayout))
	}
	if got.GrossIDR != row.GrossIDR || got.RefundIDR != row.RefundIDR || got.NetIDR != row.NetIDR ||
		got.PercentBp != row.PercentBp || got.FixedIDR != row.FixedIDR ||
		got.CommissionIDR != row.CommissionIDR || got.TotalIDR != row.TotalIDR {
		t.Errorf("snapshot = %+v, want the computed row %+v", got, row)
	}
	if got.TotalIDR == 1 {
		t.Error("a posted amount leaked into the snapshot")
	}
	if got.Note != "transfer BCA" {
		t.Errorf("note = %q, want the submitted note", got.Note)
	}
	if got.PaidBy == nil || *got.PaidBy != payrollPayAdminID {
		t.Errorf("paid_by = %v, want %v", got.PaidBy, payrollPayAdminID)
	}
	if got.PaidAt.IsZero() {
		t.Error("paid_at is zero")
	}

	// The redirect target renders the success banner.
	rec = adminGet(t, h, wantLoc)
	if !strings.Contains(rec.Body.String(), i18n.T("id", "pr.payok")) {
		t.Error("success flag does not render pr.payok")
	}
}

// HTMX posts answer with HX-Redirect (the house pattern), never a full page
// swap.
func TestAdminPayrollPayHTMXRedirect(t *testing.T) {
	report, _ := payrollTestReport(service.PayrollKindWeek)
	store := &fakePayoutStore{}
	h := newPayrollServer(t, &fakePayrollReporter{report: report}, store)

	rec := payrollPayPost(t, h, payrollPayAdmin(), payrollPayForm(t), true)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if got, want := rec.Header().Get("HX-Redirect"), "/admin/payroll?kind=week&start=2026-11-23&ok=1"; got != want {
		t.Errorf("HX-Redirect = %q, want %q", got, want)
	}
	if len(store.created) != 1 {
		t.Errorf("created %d rows, want 1", len(store.created))
	}
}

// The unique key losing a race (or a second tab) is an inline "already paid"
// message at the page's status, never a 500 and never a server-fault log.
func TestAdminPayrollPayAlreadyPaid(t *testing.T) {
	report, _ := payrollTestReport(service.PayrollKindWeek)
	store := &fakePayoutStore{createErr: &pgconn.PgError{
		Code:           "23505",
		ConstraintName: "staff_payouts_period_key",
	}}
	h := newPayrollServer(t, &fakePayrollReporter{report: report}, store)

	for _, htmx := range []bool{false, true} {
		rec := payrollPayPost(t, h, payrollPayAdmin(), payrollPayForm(t), htmx)
		if rec.Code != http.StatusOK {
			t.Errorf("htmx=%v status = %d, want 200 inline", htmx, rec.Code)
		}
		if htmx {
			if got := rec.Header().Get("HX-Reswap"); got != "none" {
				t.Errorf("HX-Reswap = %q, want none", got)
			}
			if !strings.Contains(rec.Header().Get("HX-Trigger"), i18n.T("id", "pr.already")) {
				t.Errorf("HX-Trigger = %q, want the already-paid toast", rec.Header().Get("HX-Trigger"))
			}
		} else if !strings.Contains(rec.Body.String(), i18n.T("id", "pr.already")) {
			t.Errorf("body is missing pr.already")
		}
	}
	if len(store.created) != 0 {
		t.Errorf("the duplicate submit wrote %d rows", len(store.created))
	}
	if strings.Contains(store.logs.String(), "admin payroll pay") {
		t.Errorf("an already-paid submit was logged as a server fault: %q", store.logs.String())
	}
}

// Only the period key means "already paid". A unique violation on any other
// constraint (a future schema addition, or a mis-wired test double) is a real
// fault: it must take the 500 path and be logged, never the friendly retry
// prompt that would hide a double-write.
func TestAdminPayrollPayOtherUniqueViolation(t *testing.T) {
	report, _ := payrollTestReport(service.PayrollKindWeek)
	store := &fakePayoutStore{createErr: &pgconn.PgError{
		Code:           "23505",
		ConstraintName: "some_other_unique_key",
	}}
	h := newPayrollServer(t, &fakePayrollReporter{report: report}, store)

	rec := payrollPayPost(t, h, payrollPayAdmin(), payrollPayForm(t), true)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for a foreign unique violation", rec.Code)
	}
	if got := rec.Header().Get("HX-Reswap"); got != "none" {
		t.Errorf("HX-Reswap = %q, want none", got)
	}
	if !strings.Contains(rec.Header().Get("HX-Trigger"), "toast") {
		t.Errorf("HX-Trigger = %q, want a toast", rec.Header().Get("HX-Trigger"))
	}
	if strings.Contains(rec.Header().Get("HX-Trigger"), i18n.T("id", "pr.already")) {
		t.Error("a foreign unique violation was reported as already paid")
	}
	if !strings.Contains(store.logs.String(), "admin payroll pay") {
		t.Errorf("the write failure was not logged: %q", store.logs.String())
	}
}

// R25: a payout for an unconfigured rate is meaningless, so the write is
// refused inline before it reaches the repo.
func TestAdminPayrollPayRefusesMissingRate(t *testing.T) {
	report, rows := payrollTestReport(service.PayrollKindWeek)
	store := &fakePayoutStore{}
	h := newPayrollServer(t, &fakePayrollReporter{report: report}, store)

	form := payrollPayForm(t)
	form.Set("staff", rows[1].StaffID.String()) // Bima: RateMissing
	rec := payrollPayPost(t, h, payrollPayAdmin(), form, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 inline", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), i18n.T("id", "pr.payrate")) {
		t.Error("page is missing the configure-rate copy")
	}
	if len(store.created) != 0 {
		t.Errorf("a rate-less row was paid: %d rows written", len(store.created))
	}
}

// The staff id must belong to the period's recomputed rows: an arbitrary or
// malformed id is refused inline instead of paying someone who is not there.
func TestAdminPayrollPayUnknownStaff(t *testing.T) {
	report, _ := payrollTestReport(service.PayrollKindWeek)
	store := &fakePayoutStore{}
	h := newPayrollServer(t, &fakePayrollReporter{report: report}, store)

	for _, tc := range []struct{ staff, wantKey string }{
		{uuid.New().String(), "pr.nostaff"},
		{"ayu", "pr.staff.bad"},
	} {
		form := payrollPayForm(t)
		form.Set("staff", tc.staff)
		rec := payrollPayPost(t, h, payrollPayAdmin(), form, false)
		if rec.Code != http.StatusOK {
			t.Errorf("staff %q status = %d, want 200 inline", tc.staff, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), i18n.T("id", tc.wantKey)) {
			t.Errorf("staff %q: page is missing %s", tc.staff, tc.wantKey)
		}
	}
	if len(store.created) != 0 {
		t.Errorf("unknown staff wrote %d rows", len(store.created))
	}
}

// The period is parsed strictly on the POST (the GET may default a stale
// bookmark, but a write must not guess which period it is paying).
func TestAdminPayrollPayBadPeriod(t *testing.T) {
	report, _ := payrollTestReport(service.PayrollKindWeek)
	store := &fakePayoutStore{}
	h := newPayrollServer(t, &fakePayrollReporter{report: report}, store)

	cases := []struct{ kind, start string }{
		{"quarter", "2026-11-25"},
		{"week", "2026-13-40"},
		{"", ""},
	}
	for _, tc := range cases {
		form := payrollPayForm(t)
		form.Set("kind", tc.kind)
		form.Set("start", tc.start)
		rec := payrollPayPost(t, h, payrollPayAdmin(), form, false)
		if rec.Code != http.StatusOK {
			t.Errorf("kind=%q start=%q status = %d, want 200 inline", tc.kind, tc.start, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), i18n.T("id", "pr.per.bad")) {
			t.Errorf("kind=%q start=%q: page is missing pr.per.bad", tc.kind, tc.start)
		}
	}
	if len(store.created) != 0 {
		t.Errorf("a bad period wrote %d rows", len(store.created))
	}
}

// The note is optional but bounded: 500 runes round-trip exactly, 501 is
// refused inline before the write.
func TestAdminPayrollPayNoteLimit(t *testing.T) {
	report, _ := payrollTestReport(service.PayrollKindWeek)

	t.Run("501 refused", func(t *testing.T) {
		store := &fakePayoutStore{}
		h := newPayrollServer(t, &fakePayrollReporter{report: report}, store)
		form := payrollPayForm(t)
		form.Set("note", strings.Repeat("a", 501))
		rec := payrollPayPost(t, h, payrollPayAdmin(), form, false)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 inline", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), i18n.T("id", "pr.paynote")) {
			t.Error("page is missing the note-length copy")
		}
		if len(store.created) != 0 {
			t.Errorf("an over-long note wrote %d rows", len(store.created))
		}
	})

	t.Run("500 round-trips", func(t *testing.T) {
		store := &fakePayoutStore{}
		h := newPayrollServer(t, &fakePayrollReporter{report: report}, store)
		want := strings.Repeat("a", 500)
		form := payrollPayForm(t)
		form.Set("note", want)
		rec := payrollPayPost(t, h, payrollPayAdmin(), form, false)
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("status = %d, want 303; body: %s", rec.Code, rec.Body.String())
		}
		if len(store.created) != 1 || store.created[0].Note != want {
			t.Errorf("500-rune note did not round-trip: %d rows, note len %d",
				len(store.created), len(store.created[0].Note))
		}
	})
}

// A write failure that is not the period key is a real fault: the staffFail
// contract keeps the button honest and logs the cause.
func TestAdminPayrollPayWriteFailure(t *testing.T) {
	report, _ := payrollTestReport(service.PayrollKindWeek)
	store := &fakePayoutStore{createErr: errTestPayCreate}
	h := newPayrollServer(t, &fakePayrollReporter{report: report}, store)

	rec := payrollPayPost(t, h, payrollPayAdmin(), payrollPayForm(t), true)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := rec.Header().Get("HX-Reswap"); got != "none" {
		t.Errorf("HX-Reswap = %q, want none", got)
	}
	if !strings.Contains(rec.Header().Get("HX-Trigger"), "toast") {
		t.Errorf("HX-Trigger = %q, want a toast", rec.Header().Get("HX-Trigger"))
	}
	if !strings.Contains(store.logs.String(), "admin payroll pay") {
		t.Errorf("the write failure was not logged: %q", store.logs.String())
	}
}

// The paid badge is the immutable snapshot: it shows the snapshot total even
// when a later refund moved the recomputed row, and says so quietly.
func TestAdminPayrollPaidBadgeShowsSnapshot(t *testing.T) {
	report, rows := payrollTestReport(service.PayrollKindWeek)
	ayu := rows[0].StaffID
	snapshot := &domain.StaffPayout{
		StaffID: ayu, PeriodKind: domain.PeriodWeekly,
		PeriodStart: report.Period.Start,
		TotalIDR:    60_000, // the row recomputes to 75,000 after a refund
		Note:        "transfer BCA",
		PaidAt:      time.Date(2026, 11, 30, 10, 0, 0, 0, payrollWIB),
	}
	store := &fakePayoutStore{byKey: map[string]*domain.StaffPayout{
		payrollPayoutKey(ayu, domain.PeriodWeekly, report.Period.Start): snapshot,
	}}
	h := newPayrollServer(t, &fakePayrollReporter{report: report}, store)

	rec := adminGet(t, h, "/admin/payroll?kind=week&start=2026-11-23")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	out := rec.Body.String()
	if !strings.Contains(out, i18n.T("id", "pr.paid")+" · Rp 60.000") {
		t.Errorf("paid badge does not show the snapshot total (want %q)",
			i18n.T("id", "pr.paid")+" · Rp 60.000")
	}
	if !strings.Contains(out, i18n.T("id", "pr.snapdiff")) {
		t.Error("a snapshot/recomputed difference is not explained")
	}
	if !strings.Contains(out, i18n.T("id", "pr.corr")) {
		t.Error("the correction rule line is missing")
	}
	if !strings.Contains(out, i18n.T("id", "pr.pay")) {
		t.Error("an unpaid row has no mark-paid control")
	}
}

var errTestPayCreate = testError("payout create failed")

// R27: cadence is the settlement granularity. A weekly therapist cannot be paid
// from the month view and a monthly one cannot be paid from the week view; the
// write is refused inline before any insert, in both directions.
func TestAdminPayrollPayRefusesCadenceMismatch(t *testing.T) {
	cases := []struct {
		kind, start string
	}{
		{service.PayrollKindWeek, "2026-11-25"},
		{service.PayrollKindMonth, "2026-11-15"},
	}
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			report, rows := payrollTestReport(tc.kind)
			mismatch := rows[3] // Dewi: the other cadence by construction
			store := &fakePayoutStore{}
			h := newPayrollServer(t, &fakePayrollReporter{report: report}, store)

			form := payrollPayForm(t)
			form.Set("staff", mismatch.StaffID.String())
			form.Set("kind", tc.kind)
			form.Set("start", tc.start)
			rec := payrollPayPost(t, h, payrollPayAdmin(), form, false)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 inline", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), i18n.T("id", "pr.paysettle")) {
				t.Error("the cadence-mismatch refusal copy is missing")
			}
			if len(store.created) != 0 {
				t.Errorf("a mismatched cadence wrote %d rows", len(store.created))
			}
		})
	}
}

// R26: a fixed-only retainer has a real total and is pay-capable even with zero
// sessions.
func TestAdminPayrollPayFixedOnly(t *testing.T) {
	report, rows := payrollTestReport(service.PayrollKindMonth)
	eka := rows[4]
	store := &fakePayoutStore{}
	h := newPayrollServer(t, &fakePayrollReporter{report: report}, store)

	form := payrollPayForm(t)
	form.Set("staff", eka.StaffID.String())
	form.Set("kind", service.PayrollKindMonth)
	form.Set("start", "2026-11-15")
	rec := payrollPayPost(t, h, payrollPayAdmin(), form, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}
	if len(store.created) != 1 {
		t.Fatalf("created %d rows, want 1", len(store.created))
	}
	if got := store.created[0]; got.TotalIDR != 500_000 || got.FixedIDR != 500_000 || got.GrossIDR != 0 {
		t.Errorf("fixed-only snapshot = %+v, want total/fixed 500000 and gross 0", got)
	}
}

// A future period is refused before any insert (nothing to freeze yet).
func TestAdminPayrollPayRefusesFuturePeriod(t *testing.T) {
	report, rows := payrollTestReport(service.PayrollKindMonth)
	report.Period = service.PayrollPeriod{
		Kind:  service.PayrollKindMonth,
		Start: time.Date(2099, 1, 1, 0, 0, 0, 0, payrollTestWIB()),
		End:   time.Date(2099, 1, 31, 0, 0, 0, 0, payrollTestWIB()),
	}
	store := &fakePayoutStore{}
	h := newPayrollServer(t, &fakePayrollReporter{report: report}, store)

	form := payrollPayForm(t)
	form.Set("staff", rows[2].StaffID.String())
	form.Set("kind", service.PayrollKindMonth)
	form.Set("start", "2099-01-15")
	rec := payrollPayPost(t, h, payrollPayAdmin(), form, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 inline", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), i18n.T("id", "pr.future")) {
		t.Error("the future-period refusal copy is missing")
	}
	if len(store.created) != 0 {
		t.Errorf("a future period wrote %d rows", len(store.created))
	}
}

// An empty row (no sessions, zero total) has nothing to freeze; a refund-only
// negative row is not empty and stays payable (covered by the integration
// negative-net test).
func TestAdminPayrollPayRefusesEmptyRow(t *testing.T) {
	period := payrollTestPeriod(service.PayrollKindMonth)
	staff := uuid.New()
	report := service.PayrollReport{
		Period: period,
		Rows: []service.PayrollRow{{
			StaffID: staff, StaffName: "Zero Row", Sessions: 0,
			PercentBp: 2000, Base: domain.CompBaseNet,
			Cadence: domain.CompCadenceMonthly, TotalIDR: 0,
		}},
	}
	store := &fakePayoutStore{}
	h := newPayrollServer(t, &fakePayrollReporter{report: report}, store)

	form := payrollPayForm(t)
	form.Set("staff", staff.String())
	form.Set("kind", service.PayrollKindMonth)
	form.Set("start", "2026-11-15")
	rec := payrollPayPost(t, h, payrollPayAdmin(), form, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 inline", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), i18n.T("id", "pr.nothing")) {
		t.Error("the nothing-to-pay refusal copy is missing")
	}
	if len(store.created) != 0 {
		t.Errorf("an empty row wrote %d rows", len(store.created))
	}
}
