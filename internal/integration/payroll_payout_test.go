//go:build integration

package integration

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"spa-booking/internal/config"
	"spa-booking/internal/domain"
	"spa-booking/internal/http/handlers"
	"spa-booking/internal/http/middleware"
	"spa-booking/internal/i18n"
	"spa-booking/internal/repo"
	"spa-booking/internal/service"
)

// Mark-paid is the only write in payroll, and its whole point is that the
// numbers are frozen at that moment. These tests drive the real POST through
// the real router onto a real database: the snapshot must equal the recomputed
// row, a second submit must lose to the unique key without a 500, and a refund
// recorded afterwards must move the report but never the snapshot.

// payrollPayoutServer wires the payroll handlers onto the test pool. Only the
// payroll services are supplied: the routes this test exercises never touch
// the rest of the app.
func payrollPayoutServer(t *testing.T, pool *pgxpool.Pool) (http.Handler, *repo.StaffPayoutRepo, *service.PayrollService) {
	t.Helper()
	payRepo := repo.NewPayrollRepo(pool)
	compRepo := repo.NewStaffCompRepo(pool)
	payouts := repo.NewStaffPayoutRepo(pool)
	svc := service.NewPayrollService(payRepo, compRepo)

	s, err := handlers.New(config.Config{}, slog.Default(),
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		svc, payouts, payRepo, compRepo)
	if err != nil {
		t.Fatalf("new payroll server: %v", err)
	}
	r := chi.NewRouter()
	s.Routes(r)
	return r, payouts, svc
}

// payrollPayRequest is one admin form post with a valid CSRF pair and the
// authenticated identity attached the way LoadUser would.
func payrollPayRequest(t *testing.T, h http.Handler, admin uuid.UUID, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	form.Set("csrf", "tok")
	req := httptest.NewRequest(http.MethodPost, "/admin/payroll/pay", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf", Value: "tok"})
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserKey,
		&domain.User{ID: admin, Role: domain.RoleAdmin, FullName: "Payroll Admin"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// payrollPageGet reads the period page as the admin, for the paid-badge
// assertions.
func payrollPageGet(t *testing.T, h http.Handler, admin uuid.UUID, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserKey,
		&domain.User{ID: admin, Role: domain.RoleAdmin, FullName: "Payroll Admin"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// payrollVoidRequest posts the void form through the real router.
func payrollVoidRequest(t *testing.T, h http.Handler, admin uuid.UUID, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	form.Set("csrf", "tok")
	req := httptest.NewRequest(http.MethodPost, "/admin/payroll/payout/void", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf", Value: "tok"})
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserKey,
		&domain.User{ID: admin, Role: domain.RoleAdmin, FullName: "Payroll Admin"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func payrollPayoutCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM staff_payouts`).Scan(&n); err != nil {
		t.Fatalf("count payouts: %v", err)
	}
	return n
}

// payrollSeedPayable creates one branch, service, therapist with a 20% net
// rate, one completed November visit (200,000 less a 50,000 discount) and an
// admin account. The expected commission is 30,000 on a 150,000 net.
func payrollSeedPayable(t *testing.T, pool *pgxpool.Pool) (staffID, adminID uuid.UUID, bookingID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	loc := insertLocation(t, pool, "Payout Branch")
	svc := insertService(t, pool, loc, "Payout Treatment", 200_000)
	staffID = insertStaff(t, pool, loc, "Payout Therapist")
	adminID = insertUser(t, pool, "payout-admin@example.com", string(domain.RoleAdmin))

	if err := repo.NewStaffCompRepo(pool).Create(ctx, &domain.StaffComp{
		StaffID:        staffID,
		PercentBp:      2000,
		Cadence:        domain.CompCadenceMonthly,
		CommissionBase: domain.CompBaseNet,
		EffectiveFrom:  payrollDate(2026, time.January, 1),
	}); err != nil {
		t.Fatalf("seed rate: %v", err)
	}
	bookingID = insertBooking(t, pool, bookingSeed{
		LocationID:  loc,
		ServiceID:   svc,
		StaffID:     staffID,
		StartAt:     time.Date(2026, time.November, 24, 10, 0, 0, 0, wib),
		Status:      string(domain.BookingCompleted),
		PriceIDR:    200_000,
		DiscountIDR: 50_000,
	})
	return staffID, adminID, bookingID
}

// TestPayrollMarkPaidSnapshot pins the whole mark-paid path: the redirect, the
// one stored row, and every frozen field against the report the handler
// recomputed. A posted amount in the form must not reach the row.
func TestPayrollMarkPaidSnapshot(t *testing.T) {
	pool := OpenTestDB(t)
	TruncateAll(t, pool)
	staffID, adminID, _ := payrollSeedPayable(t, pool)
	h, payouts, svc := payrollPayoutServer(t, pool)

	periodStart := payrollDate(2026, time.November, 1)
	form := url.Values{
		"staff": {staffID.String()},
		"kind":  {"month"},
		"start": {"2026-11-15"}, // normalises to the first of the month
		"note":  {"transfer BCA"},
		"total": {"1"}, // never trusted
	}
	rec := payrollPayRequest(t, h, adminID, form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}
	wantLoc := "/admin/payroll?kind=month&start=2026-11-01&ok=1"
	if got := rec.Header().Get("Location"); got != wantLoc {
		t.Errorf("Location = %q, want %q", got, wantLoc)
	}

	report, err := svc.Report(context.Background(), service.PayrollKindMonth, periodStart)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	row := payrollRowNamed(t, report, "Payout Therapist")

	got, err := payouts.ForPeriod(context.Background(), staffID, domain.PeriodMonthly, periodStart)
	if err != nil {
		t.Fatalf("ForPeriod: %v", err)
	}
	if got == nil {
		t.Fatal("no payout row was written")
	}
	if got.PeriodKind != domain.PeriodMonthly ||
		got.PeriodStart.Format("2006-01-02") != "2026-11-01" ||
		got.PeriodEnd.Format("2006-01-02") != "2026-11-30" {
		t.Errorf("period = %q %s..%s, want monthly 2026-11-01..2026-11-30",
			got.PeriodKind, got.PeriodStart.Format("2006-01-02"), got.PeriodEnd.Format("2006-01-02"))
	}
	if got.GrossIDR != row.GrossIDR || got.RefundIDR != row.RefundIDR || got.NetIDR != row.NetIDR ||
		got.PercentBp != row.PercentBp || got.FixedIDR != row.FixedIDR ||
		got.CommissionIDR != row.CommissionIDR || got.TotalIDR != row.TotalIDR {
		t.Errorf("snapshot drifted from the computed row:\nsnapshot: %+v\nrow:      %+v", got, row)
	}
	if got.GrossIDR != 150_000 || got.RefundIDR != 0 || got.NetIDR != 150_000 ||
		got.PercentBp != 2000 || got.CommissionIDR != 30_000 || got.TotalIDR != 30_000 {
		t.Errorf("snapshot money = gross %d refund %d net %d bp %d comm %d total %d, want 150000/0/150000/2000/30000/30000",
			got.GrossIDR, got.RefundIDR, got.NetIDR, got.PercentBp, got.CommissionIDR, got.TotalIDR)
	}
	if got.Note != "transfer BCA" {
		t.Errorf("note = %q, want transfer BCA", got.Note)
	}
	if got.PaidBy == nil || *got.PaidBy != adminID {
		t.Errorf("paid_by = %v, want %v", got.PaidBy, adminID)
	}
	if got.PaidAt.IsZero() {
		t.Error("paid_at is zero")
	}
	if n := payrollPayoutCount(t, pool); n != 1 {
		t.Fatalf("payout rows = %d, want 1", n)
	}

	// The page reads the snapshot back: it must show the paid badge with the
	// frozen total, not an "unpaid" label.
	page := payrollPageGet(t, h, adminID, "/admin/payroll?kind=month&start=2026-11-01")
	if page.Code != http.StatusOK {
		t.Fatalf("page status = %d, want 200", page.Code)
	}
	if !strings.Contains(page.Body.String(), i18n.T("id", "pr.paid")+" · Rp 30.000") {
		t.Error("the paid badge does not render the snapshot total")
	}

	// A second submit is refused as already paid — inline, not a 500 — and
	// the database still holds exactly one row.
	second := payrollPayRequest(t, h, adminID, url.Values{
		"staff": {staffID.String()},
		"kind":  {"month"},
		"start": {"2026-11-01"},
		"note":  {"double click"},
	})
	if second.Code != http.StatusOK {
		t.Errorf("second submit status = %d, want 200 inline", second.Code)
	}
	if !strings.Contains(second.Body.String(), i18n.T("id", "pr.already")) {
		t.Error("second submit does not explain that the period is already paid")
	}
	if n := payrollPayoutCount(t, pool); n != 1 {
		t.Fatalf("payout rows after a duplicate submit = %d, want 1", n)
	}
}

// TestPayrollCadenceBlocksDoublePay pins R27 on real rows: a weekly therapist
// paid on the week view cannot be paid again from the month view (and the
// mirror), because the two snapshots live under different period kinds and
// would never see each other.
func TestPayrollCadenceBlocksDoublePay(t *testing.T) {
	pool := OpenTestDB(t)
	TruncateAll(t, pool)
	ctx := context.Background()
	h, _, _ := payrollPayoutServer(t, pool)

	loc := insertLocation(t, pool, "Cadence Branch")
	svcID := insertService(t, pool, loc, "Cadence Treatment", 100_000)
	weekly := insertStaff(t, pool, loc, "Weekly Therapist")
	monthly := insertStaff(t, pool, loc, "Monthly Therapist")
	admin := insertUser(t, pool, "cadence-admin@example.com", string(domain.RoleAdmin))
	comps := repo.NewStaffCompRepo(pool)
	for _, seed := range []struct {
		staff   uuid.UUID
		cadence string
	}{
		{weekly, domain.CompCadenceWeekly},
		{monthly, domain.CompCadenceMonthly},
	} {
		if err := comps.Create(ctx, &domain.StaffComp{
			StaffID: seed.staff, PercentBp: 2000, Cadence: seed.cadence,
			CommissionBase: domain.CompBaseNet, EffectiveFrom: payrollDate(2026, time.January, 1),
		}); err != nil {
			t.Fatalf("seed rate: %v", err)
		}
	}
	insertBooking(t, pool, bookingSeed{
		LocationID: loc, ServiceID: svcID, StaffID: weekly,
		StartAt: time.Date(2026, time.November, 24, 10, 0, 0, 0, wib),
		Status:  string(domain.BookingCompleted), PriceIDR: 100_000,
	})
	insertBooking(t, pool, bookingSeed{
		LocationID: loc, ServiceID: svcID, StaffID: monthly,
		StartAt: time.Date(2026, time.November, 24, 11, 0, 0, 0, wib),
		Status:  string(domain.BookingCompleted), PriceIDR: 100_000,
	})

	weekPay := url.Values{"staff": {weekly.String()}, "kind": {"week"}, "start": {"2026-11-25"}}
	rec := payrollPayRequest(t, h, admin, weekPay)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("weekly week pay = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}
	monthAttempt := url.Values{"staff": {weekly.String()}, "kind": {"month"}, "start": {"2026-11-15"}}
	rec = payrollPayRequest(t, h, admin, monthAttempt)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), i18n.T("id", "pr.paysettle")) {
		t.Fatalf("weekly month attempt = %d, want the inline cadence refusal; body: %s", rec.Code, rec.Body.String())
	}
	if n := payrollPayoutCount(t, pool); n != 1 {
		t.Fatalf("payout rows after the refused cross-kind pay = %d, want 1", n)
	}

	monthPay := url.Values{"staff": {monthly.String()}, "kind": {"month"}, "start": {"2026-11-15"}}
	rec = payrollPayRequest(t, h, admin, monthPay)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("monthly month pay = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}
	weekAttempt := url.Values{"staff": {monthly.String()}, "kind": {"week"}, "start": {"2026-11-25"}}
	rec = payrollPayRequest(t, h, admin, weekAttempt)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), i18n.T("id", "pr.paysettle")) {
		t.Fatalf("monthly week attempt = %d, want the inline cadence refusal; body: %s", rec.Code, rec.Body.String())
	}
	if n := payrollPayoutCount(t, pool); n != 2 {
		t.Fatalf("payout rows after the mirror = %d, want 2 (one per cadence)", n)
	}
}

// TestPayrollVoidAndRemark pins R28 end to end: voiding requires a reason at
// the handler, the period then reads unpaid, a re-mark writes a second row, the
// voided row stays in history, and the partial unique index still refuses a
// second LIVE mark.
func TestPayrollVoidAndRemark(t *testing.T) {
	pool := OpenTestDB(t)
	TruncateAll(t, pool)
	staffID, adminID, _ := payrollSeedPayable(t, pool)
	h, payouts, _ := payrollPayoutServer(t, pool)
	ctx := context.Background()
	periodStart := payrollDate(2026, time.November, 1)

	rec := payrollPayRequest(t, h, adminID, url.Values{
		"staff": {staffID.String()}, "kind": {"month"}, "start": {"2026-11-01"},
		"note": {"first mark"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("first pay = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}
	first, err := payouts.ForPeriod(ctx, staffID, domain.PeriodMonthly, periodStart)
	if err != nil || first == nil {
		t.Fatalf("first snapshot = (%+v, %v)", first, err)
	}

	// No reason = no void.
	rec = payrollVoidRequest(t, h, adminID, url.Values{
		"id": {first.ID.String()}, "kind": {"month"}, "start": {"2026-11-01"}, "reason": {"  "},
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), i18n.T("id", "pr.voidbad")) {
		t.Fatalf("reasonless void = %d, want the inline reason refusal; body: %s", rec.Code, rec.Body.String())
	}
	if got, _ := payouts.ForPeriod(ctx, staffID, domain.PeriodMonthly, periodStart); got == nil {
		t.Fatal("a refused void marked the period unpaid")
	}

	rec = payrollVoidRequest(t, h, adminID, url.Values{
		"id": {first.ID.String()}, "kind": {"month"}, "start": {"2026-11-01"},
		"reason": {"wrong amount"},
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), i18n.T("id", "pr.voidok")) {
		t.Fatalf("void = %d, want the success copy; body: %s", rec.Code, rec.Body.String())
	}
	if got, _ := payouts.ForPeriod(ctx, staffID, domain.PeriodMonthly, periodStart); got != nil {
		t.Fatalf("ForPeriod after void = %+v, want nil (voided rows never count as paid)", got)
	}
	hist, err := payouts.HistoryFor(ctx, staffID, domain.PeriodMonthly, periodStart)
	if err != nil || len(hist) != 1 {
		t.Fatalf("history after void = (%d rows, %v), want 1", len(hist), err)
	}
	if hist[0].ID != first.ID || hist[0].VoidedAt == nil || hist[0].VoidReason != "wrong amount" {
		t.Fatalf("voided history row = %+v, want the first snapshot voided with its reason", hist[0])
	}
	if hist[0].VoidedBy == nil || *hist[0].VoidedBy != adminID {
		t.Errorf("voided_by = %v, want %v", hist[0].VoidedBy, adminID)
	}

	// The period can be marked again; the new snapshot is a new row.
	rec = payrollPayRequest(t, h, adminID, url.Values{
		"staff": {staffID.String()}, "kind": {"month"}, "start": {"2026-11-01"},
		"note": {"re-marked"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("re-mark = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}
	second, err := payouts.ForPeriod(ctx, staffID, domain.PeriodMonthly, periodStart)
	if err != nil || second == nil {
		t.Fatalf("second snapshot = (%+v, %v)", second, err)
	}
	if second.ID == first.ID {
		t.Error("the re-mark reused the voided snapshot's id; want a new row")
	}
	hist, err = payouts.HistoryFor(ctx, staffID, domain.PeriodMonthly, periodStart)
	if err != nil || len(hist) != 2 {
		t.Fatalf("history after re-mark = (%d rows, %v), want 2", len(hist), err)
	}
	live := 0
	for _, p := range hist {
		if p.VoidedAt == nil {
			live++
		}
	}
	if live != 1 {
		t.Errorf("live snapshots in history = %d, want exactly 1", live)
	}

	// The partial unique index still refuses a second LIVE row for the period.
	dup := *second
	dup.ID = uuid.Nil
	err = payouts.Create(ctx, &dup)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" || pgErr.ConstraintName != "staff_payouts_period_key" {
		t.Fatalf("second live mark error = %v, want 23505 on staff_payouts_period_key", err)
	}

	// The page shows the paid badge plus the voided history with its reason.
	page := payrollPageGet(t, h, adminID, "/admin/payroll?kind=month&start=2026-11-01")
	if page.Code != http.StatusOK {
		t.Fatalf("page status = %d, want 200", page.Code)
	}
	out := page.Body.String()
	if !strings.Contains(out, i18n.T("id", "pr.voided")) || !strings.Contains(out, "wrong amount") {
		t.Error("the page does not show the voided snapshot and its reason")
	}
}

// TestPayrollFixedOnlyPayableOverSQL pins R26 against the real roster read: an
// active monthly retainer with no visits appears on the month view and can be
// paid; a weekly one appears on the week view; an inactive one does not appear.
func TestPayrollFixedOnlyPayableOverSQL(t *testing.T) {
	pool := OpenTestDB(t)
	TruncateAll(t, pool)
	ctx := context.Background()

	loc := insertLocation(t, pool, "Retainer Branch")
	monthly := insertStaff(t, pool, loc, "Monthly Retainer")
	weekly := insertStaff(t, pool, loc, "Weekly Retainer")
	inactive := insertStaff(t, pool, loc, "Inactive Retainer")
	admin := insertUser(t, pool, "retainer-admin@example.com", string(domain.RoleAdmin))
	if _, err := pool.Exec(ctx, `UPDATE staff SET active=false WHERE id=$1`, inactive); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	comps := repo.NewStaffCompRepo(pool)
	for _, seed := range []struct {
		staff   uuid.UUID
		fixed   int64
		cadence string
	}{
		{monthly, 500_000, domain.CompCadenceMonthly},
		{weekly, 300_000, domain.CompCadenceWeekly},
		{inactive, 900_000, domain.CompCadenceMonthly},
	} {
		if err := comps.Create(ctx, &domain.StaffComp{
			StaffID: seed.staff, FixedIDR: seed.fixed, Cadence: seed.cadence,
			CommissionBase: domain.CompBaseNet, EffectiveFrom: payrollDate(2026, time.January, 1),
		}); err != nil {
			t.Fatalf("seed retainer: %v", err)
		}
	}

	h, _, svc := payrollPayoutServer(t, pool)
	month, err := svc.Report(ctx, service.PayrollKindMonth, payrollDate(2026, time.November, 1))
	if err != nil {
		t.Fatalf("month report: %v", err)
	}
	row := payrollRowNamed(t, month, "Monthly Retainer")
	if !row.FixedOnly || row.TotalIDR != 500_000 || row.Sessions != 0 {
		t.Fatalf("monthly retainer row = %+v, want FixedOnly total 500000, zero sessions", row)
	}
	for _, name := range []string{"Weekly Retainer", "Inactive Retainer"} {
		for _, r := range month.Rows {
			if r.StaffName == name {
				t.Errorf("month view invented a row for %q", name)
			}
		}
	}
	week, err := svc.Report(ctx, service.PayrollKindWeek, payrollDate(2026, time.November, 23))
	if err != nil {
		t.Fatalf("week report: %v", err)
	}
	if got := payrollRowNamed(t, week, "Weekly Retainer"); !got.FixedOnly || got.TotalIDR != 300_000 {
		t.Fatalf("weekly retainer week row = %+v, want FixedOnly total 300000", got)
	}

	// The retainer can be paid: a fixed-only row has a real total to freeze.
	rec := payrollPayRequest(t, h, admin, url.Values{
		"staff": {monthly.String()}, "kind": {"month"}, "start": {"2026-11-01"},
		"note": {"retainer"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("retainer pay = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}
	got, err := repo.NewStaffPayoutRepo(pool).ForPeriod(ctx, monthly, domain.PeriodMonthly, payrollDate(2026, time.November, 1))
	if err != nil || got == nil {
		t.Fatalf("retainer snapshot = (%+v, %v)", got, err)
	}
	if got.GrossIDR != 0 || got.FixedIDR != 500_000 || got.TotalIDR != 500_000 {
		t.Errorf("retainer snapshot = gross %d fixed %d total %d, want 0/500000/500000",
			got.GrossIDR, got.FixedIDR, got.TotalIDR)
	}
}

// A refund recorded after the period was paid moves the recomputed report but
// never the snapshot; the page explains the difference and keeps the frozen
// figure.
func TestPayrollSnapshotSurvivesLateRefund(t *testing.T) {
	pool := OpenTestDB(t)
	TruncateAll(t, pool)
	staffID, adminID, bookingID := payrollSeedPayable(t, pool)
	h, payouts, svc := payrollPayoutServer(t, pool)
	ctx := context.Background()
	periodStart := payrollDate(2026, time.November, 1)

	rec := payrollPayRequest(t, h, adminID, url.Values{
		"staff": {staffID.String()},
		"kind":  {"month"},
		"start": {"2026-11-01"},
		"note":  {"paid before the refund"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("pay status = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}
	before, err := payouts.ForPeriod(ctx, staffID, domain.PeriodMonthly, periodStart)
	if err != nil || before == nil {
		t.Fatalf("snapshot before refund = (%+v, %v)", before, err)
	}

	// A 50,000 refund on the completed visit: the report's commission drops
	// from 20% of 150,000 (30,000) to 20% of 100,000 (20,000).
	seedPayrollRefund(t, pool, ctx, bookingID, 50_000,
		time.Date(2026, time.November, 26, 12, 0, 0, 0, wib), "late refund")

	after, err := payouts.ForPeriod(ctx, staffID, domain.PeriodMonthly, periodStart)
	if err != nil || after == nil {
		t.Fatalf("snapshot after refund = (%+v, %v)", after, err)
	}
	if after.RefundIDR != before.RefundIDR || after.NetIDR != before.NetIDR || after.TotalIDR != before.TotalIDR {
		t.Errorf("the snapshot changed after a late refund:\nbefore: %+v\nafter:  %+v", before, after)
	}
	if after.RefundIDR != 0 || after.TotalIDR != 30_000 {
		t.Errorf("snapshot refund/total = %d/%d, want 0/30000", after.RefundIDR, after.TotalIDR)
	}

	report, err := svc.Report(ctx, service.PayrollKindMonth, periodStart)
	if err != nil {
		t.Fatalf("report after refund: %v", err)
	}
	row := payrollRowNamed(t, report, "Payout Therapist")
	if row.RefundIDR != 50_000 || row.TotalIDR != 20_000 || row.TotalIDR == before.TotalIDR {
		t.Errorf("recomputed row = refund %d total %d, want 50000/20000 (snapshot was 30000)",
			row.RefundIDR, row.TotalIDR)
	}

	page := payrollPageGet(t, h, adminID, "/admin/payroll?kind=month&start=2026-11-01")
	if page.Code != http.StatusOK {
		t.Fatalf("page status = %d, want 200", page.Code)
	}
	out := page.Body.String()
	if !strings.Contains(out, i18n.T("id", "pr.snapdiff")) {
		t.Error("the page does not explain the snapshot/recomputed difference")
	}
	if !strings.Contains(out, i18n.T("id", "pr.paid")+" · Rp 30.000") {
		t.Error("the page does not show the frozen snapshot total")
	}
}

// R25: a therapist with no rate row has no meaningful commission, so the write
// is refused inline and no snapshot is stored.
func TestPayrollMarkPaidRefusesMissingRate(t *testing.T) {
	pool := OpenTestDB(t)
	TruncateAll(t, pool)
	ctx := context.Background()
	loc := insertLocation(t, pool, "No-rate Branch")
	svc := insertService(t, pool, loc, "No-rate Treatment", 150_000)
	staffID := insertStaff(t, pool, loc, "No Rate Therapist")
	adminID := insertUser(t, pool, "norate-admin@example.com", string(domain.RoleAdmin))
	// A completed visit is enough to put her on the report; no staff_comp row
	// means RateMissing.
	insertBooking(t, pool, bookingSeed{
		LocationID: loc,
		ServiceID:  svc,
		StaffID:    staffID,
		StartAt:    time.Date(2026, time.November, 25, 10, 0, 0, 0, wib),
		Status:     string(domain.BookingCompleted),
		PriceIDR:   150_000,
	})
	h, _, _ := payrollPayoutServer(t, pool)

	rec := payrollPayRequest(t, h, adminID, url.Values{
		"staff": {staffID.String()},
		"kind":  {"month"},
		"start": {"2026-11-01"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 inline", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), i18n.T("id", "pr.payrate")) {
		t.Error("the rate-less refusal copy is missing")
	}
	if n := payrollPayoutCount(t, pool); n != 0 {
		t.Fatalf("payout rows = %d, want 0 for a missing rate", n)
	}
	if _, err := repo.NewStaffPayoutRepo(pool).ForPeriod(ctx, staffID, domain.PeriodMonthly,
		payrollDate(2026, time.November, 1)); err != nil {
		t.Fatalf("ForPeriod: %v", err)
	}
}

// A refund-only period has no visits and a negative net. Marking it paid must
// store the signed figure: the schema deliberately has no net_idr >= 0 check
// because this is exactly the R17 case.
func TestPayrollMarkPaidNegativeNet(t *testing.T) {
	pool := OpenTestDB(t)
	TruncateAll(t, pool)
	staffID, adminID, bookingID := payrollSeedPayable(t, pool)
	h, payouts, _ := payrollPayoutServer(t, pool)
	ctx := context.Background()

	// The visit is September's, and the refund is recorded in October: October
	// has no visit, only the deduction, and is safely in the past.
	if _, err := pool.Exec(ctx, `UPDATE bookings SET start_at=$2::timestamptz, end_at=$2::timestamptz + interval '1 hour' WHERE id=$1`,
		bookingID, time.Date(2026, time.September, 24, 10, 0, 0, 0, wib)); err != nil {
		t.Fatalf("move visit to September: %v", err)
	}
	seedPayrollRefund(t, pool, ctx, bookingID, 50_000,
		time.Date(2026, time.October, 2, 12, 0, 0, 0, wib), "late refund")

	rec := payrollPayRequest(t, h, adminID, url.Values{
		"staff": {staffID.String()},
		"kind":  {"month"},
		"start": {"2026-10-01"},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}
	got, err := payouts.ForPeriod(ctx, staffID, domain.PeriodMonthly, payrollDate(2026, time.October, 1))
	if err != nil || got == nil {
		t.Fatalf("snapshot = (%+v, %v)", got, err)
	}
	if got.GrossIDR != 0 || got.RefundIDR != 50_000 || got.NetIDR != -50_000 ||
		got.CommissionIDR != -10_000 || got.TotalIDR != -10_000 {
		t.Errorf("negative-net snapshot = gross %d refund %d net %d comm %d total %d, want 0/50000/-50000/-10000/-10000",
			got.GrossIDR, got.RefundIDR, got.NetIDR, got.CommissionIDR, got.TotalIDR)
	}
}

// The note is optional, stored verbatim at the 500-rune ceiling, and refused
// one rune past it.
func TestPayrollMarkPaidNoteLimit(t *testing.T) {
	pool := OpenTestDB(t)
	TruncateAll(t, pool)
	staffID, adminID, _ := payrollSeedPayable(t, pool)
	h, payouts, _ := payrollPayoutServer(t, pool)
	ctx := context.Background()
	periodStart := payrollDate(2026, time.November, 1)

	tooLong := strings.Repeat("a", 501)
	rec := payrollPayRequest(t, h, adminID, url.Values{
		"staff": {staffID.String()}, "kind": {"month"}, "start": {"2026-11-01"},
		"note": {tooLong},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("501-rune note status = %d, want 200 inline", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), i18n.T("id", "pr.paynote")) {
		t.Error("the note-length refusal copy is missing")
	}
	if n := payrollPayoutCount(t, pool); n != 0 {
		t.Fatalf("an over-long note wrote %d rows", n)
	}

	want := strings.Repeat("a", 500)
	rec = payrollPayRequest(t, h, adminID, url.Values{
		"staff": {staffID.String()}, "kind": {"month"}, "start": {"2026-11-01"},
		"note": {want},
	})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("500-rune note status = %d, want 303; body: %s", rec.Code, rec.Body.String())
	}
	got, err := payouts.ForPeriod(ctx, staffID, domain.PeriodMonthly, periodStart)
	if err != nil || got == nil {
		t.Fatalf("snapshot = (%+v, %v)", got, err)
	}
	if got.Note != want {
		t.Errorf("stored note len = %d, want exactly 500", len(got.Note))
	}
}
