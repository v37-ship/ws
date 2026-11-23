//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"
	"spa-booking/internal/service"
)

// The refund window and the cash-basis monthly aggregation are the two money
// rules a unit test cannot reach: the ceiling reads RefundedTotal back from the
// refunds ledger, and the report buckets payments by paid_at and refunds by
// created_at (Asia/Jakarta) in SQL. These groups execute the real ReportService
// over real rows, so a repo-side WHERE clause or month expression drifting
// fails here instead of in the portal.

// wib is the reporting clock: every month bucket is Asia/Jakarta, so fixtures
// and assertions parse "YYYY-MM" in that zone rather than in UTC.
var wib = time.FixedZone("WIB", 7*60*60)

// moneyFixture is one branch/treatment/therapist/customer plus a CS account to
// act as the refund actor, and the real report repos the assertions read.
type moneyFixture struct {
	pool     *pgxpool.Pool
	svc      *service.ReportService
	reports  *repo.ReportRepo
	bookings *repo.BookingRepo
	loc      uuid.UUID
	service  uuid.UUID
	staff    uuid.UUID
	customer uuid.UUID
	actor    uuid.UUID
	seq      int
}

// newMoneyFixture truncates the schema and seeds one of everything the report
// SQL joins (booking -> service/staff/location/users).
func newMoneyFixture(t *testing.T) *moneyFixture {
	t.Helper()
	pool := OpenTestDB(t)
	TruncateAll(t, pool)

	loc := insertLocation(t, pool, "Report Branch")
	return &moneyFixture{
		pool: pool,
		// Wired exactly as cmd/server does; refunds read the bookings repo for
		// the activity rows and the payments repo for the paid ceiling.
		svc:      service.NewReportService(repo.NewReportRepo(pool), repo.NewBookingRepo(pool), repo.NewPaymentRepo(pool)),
		reports:  repo.NewReportRepo(pool),
		bookings: repo.NewBookingRepo(pool),
		loc:      loc,
		service:  insertService(t, pool, loc, "Report Treatment", 150_000),
		staff:    insertStaff(t, pool, loc, "Report Therapist"),
		customer: insertUser(t, pool, "report@example.com", string(domain.RoleCustomer)),
		actor:    insertUser(t, pool, "report-cs@example.com", string(domain.RoleCS)),
	}
}

// paidBooking seeds one booking plus one paid payment and returns both ids.
// paidAt is required and explicit: the report buckets by it, so callers own the
// month. Starts are spaced two hours apart so the staff no-double-book
// exclusion never sees an overlap within one fixture.
func (fx *moneyFixture) paidBooking(t *testing.T, amountIDR int64, paidAt time.Time, status domain.BookingStatus) (uuid.UUID, uuid.UUID) {
	t.Helper()
	if paidAt.IsZero() {
		t.Fatal("paidBooking: paidAt is required")
	}
	fx.seq++
	start := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC).Add(time.Duration(fx.seq) * 2 * time.Hour)
	customer := fx.customer
	bookingID := insertBooking(t, fx.pool, bookingSeed{
		CustomerID: &customer,
		LocationID: fx.loc,
		ServiceID:  fx.service,
		StaffID:    fx.staff,
		StartAt:    start,
		PriceIDR:   amountIDR,
		Status:     string(status),
	})
	paid := paidAt
	paymentID := insertPayment(t, fx.pool, paymentSeed{BookingID: bookingID, AmountIDR: amountIDR, PaidAt: &paid})
	return bookingID, paymentID
}

// pinPaidAt rewrites a seeded payment's paid_at. ReportService stamps refunds at
// now() and exposes no paid_at hook, so a test that needs both sides of a
// refund to sit in explicit months sets the payment side here — the same column
// insertPayment fills at seed time. Nothing under test reads paid_at except the
// report's month bucket.
func (fx *moneyFixture) pinPaidAt(t *testing.T, paymentID uuid.UUID, paidAt time.Time) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := fx.pool.Exec(ctx, `UPDATE payments SET paid_at=$2 WHERE id=$1`, paymentID, paidAt); err != nil {
		t.Fatalf("pin paid_at: %v", err)
	}
}

// month runs the real MonthlyReport for one YYYY-MM at this fixture's branch.
func (fx *moneyFixture) month(t *testing.T, month string) (domain.ReportSummary, []domain.ReportLine, []domain.ReportRefund) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sum, lines, refunds, err := fx.svc.MonthlyReport(ctx, month, fx.loc)
	if err != nil {
		t.Fatalf("monthly report %s: %v", month, err)
	}
	return sum, lines, refunds
}

// refundedTotal reads the ceiling input the service uses: active refunds only.
func (fx *moneyFixture) refundedTotal(t *testing.T, bookingID uuid.UUID) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	total, err := fx.reports.RefundedTotal(ctx, bookingID)
	if err != nil {
		t.Fatalf("refunded total: %v", err)
	}
	return total
}

// countRefunds counts every refund row for a booking, voided included, so a
// refused refund can be proven to have written nothing.
func (fx *moneyFixture) countRefunds(t *testing.T, bookingID uuid.UUID) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var n int
	if err := fx.pool.QueryRow(ctx, `SELECT count(*) FROM refunds WHERE booking_id=$1`, bookingID).Scan(&n); err != nil {
		t.Fatalf("count refunds: %v", err)
	}
	return n
}

// monthOf names the Asia/Jakarta month a timestamp falls in — exactly the
// bucket the report SQL assigns it to (paid_at for payments, created_at for
// refunds). Deriving assertions from the row's own timestamp keeps the tests
// independent of the wall clock.
func monthOf(t *testing.T, ts time.Time) string {
	t.Helper()
	if ts.IsZero() {
		t.Fatal("monthOf: zero timestamp")
	}
	return ts.In(wib).Format("2006-01")
}

// wibMidMonth is a safe instant inside a given YYYY-MM: the 15th at 10:00 WIB,
// far from either boundary whatever the month length.
func wibMidMonth(t *testing.T, month string) time.Time {
	t.Helper()
	start, err := time.ParseInLocation("2006-01", month, wib)
	if err != nil {
		t.Fatalf("wibMidMonth(%q): %v", month, err)
	}
	return start.AddDate(0, 0, 14).Add(10 * time.Hour)
}

// assertMonth pins a monthly summary field by field.
func assertMonth(t *testing.T, sum domain.ReportSummary, month string, gross, refund, net int64, count, refundCnt int) {
	t.Helper()
	if sum.Month != month {
		t.Errorf("summary month = %q, want %q", sum.Month, month)
	}
	if sum.GrossIDR != gross {
		t.Errorf("%s gross = %d, want %d", month, sum.GrossIDR, gross)
	}
	if sum.RefundIDR != refund {
		t.Errorf("%s refund = %d, want %d", month, sum.RefundIDR, refund)
	}
	if sum.NetIDR != net {
		t.Errorf("%s net = %d, want %d", month, sum.NetIDR, net)
	}
	if sum.Count != count {
		t.Errorf("%s paid count = %d, want %d", month, sum.Count, count)
	}
	if sum.RefundCnt != refundCnt {
		t.Errorf("%s refund count = %d, want %d", month, sum.RefundCnt, refundCnt)
	}
}

// assertNoRefunds proves a month's report carries no refund row (the voided
// exclusion lives in MonthlyRefunds' SQL, not in the service fold).
func assertNoRefunds(t *testing.T, month string, refunds []domain.ReportRefund) {
	t.Helper()
	if len(refunds) != 0 {
		t.Fatalf("%s report refunds = %d rows, want 0: %+v", month, len(refunds), refunds)
	}
}

// TestRefundCeiling executes the refund window: half the paid amount records,
// one rupiah over is refused with the exact ceiling copy and no row, and the
// exact paid amount is still reachable in two halves.
func TestRefundCeiling(t *testing.T) {
	fx := newMoneyFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bookingID, _ := fx.paidBooking(t, 150_000, time.Now(), "")

	rf, err := fx.svc.RecordRefund(ctx, bookingID, 75_000, "half back", &fx.actor)
	if err != nil {
		t.Fatalf("refund half: %v", err)
	}
	if rf.AmountIDR != 75_000 || rf.Reason != "half back" {
		t.Errorf("recorded refund = %+v, want amount 75000 and the reason", rf)
	}
	if rf.CreatedByID == nil || *rf.CreatedByID != fx.actor {
		t.Errorf("recorded refund actor = %v, want the seeded CS account", rf.CreatedByID)
	}
	if got := fx.refundedTotal(t, bookingID); got != 75_000 {
		t.Errorf("refunded total after half = %d, want 75000", got)
	}
	if !hasAction(fx.mustLogs(t, bookingID), "Refund recorded Rp 75000") {
		t.Errorf("refund did not write its activity row: %v", logActions(fx.mustLogs(t, bookingID)))
	}

	// One rupiah past the paid amount is the ceiling, with the exact copy the
	// portal shows.
	_, err = fx.svc.RecordRefund(ctx, bookingID, 75_001, "one over", &fx.actor)
	if !errors.Is(err, service.ErrValidation) {
		t.Fatalf("over-ceiling refund error = %v, want ErrValidation", err)
	}
	const wantCeiling = "validation error: refund exceeds paid amount (paid 150000, refunded 75000)"
	if err.Error() != wantCeiling {
		t.Errorf("over-ceiling message = %q, want %q", err.Error(), wantCeiling)
	}
	if got := fx.countRefunds(t, bookingID); got != 1 {
		t.Errorf("refunds after the refused over-ceiling try = %d, want 1", got)
	}

	// The whole paid amount remains reachable; the border is exact.
	if _, err := fx.svc.RecordRefund(ctx, bookingID, 75_000, "second half", &fx.actor); err != nil {
		t.Fatalf("second half at the exact ceiling: %v", err)
	}
	_, err = fx.svc.RecordRefund(ctx, bookingID, 1, "one over again", &fx.actor)
	if !errors.Is(err, service.ErrValidation) {
		t.Fatalf("refund beyond a fully refunded booking = %v, want ErrValidation", err)
	}
	const wantFull = "validation error: refund exceeds paid amount (paid 150000, refunded 150000)"
	if err.Error() != wantFull {
		t.Errorf("fully-refunded message = %q, want %q", err.Error(), wantFull)
	}
	if got := fx.countRefunds(t, bookingID); got != 2 {
		t.Errorf("refunds after the boundary walk = %d, want 2", got)
	}
	if got := fx.refundedTotal(t, bookingID); got != 150_000 {
		t.Errorf("refunded total at the ceiling = %d, want 150000", got)
	}

	// Positivity is the fast path: it runs before any row is loaded.
	_, err = fx.svc.RecordRefund(ctx, bookingID, 0, "nothing", &fx.actor)
	const wantPositive = "validation error: refund amount must be positive"
	if !errors.Is(err, service.ErrValidation) || err.Error() != wantPositive {
		t.Errorf("zero refund = (%v), want %q", err, wantPositive)
	}

	// A booking that never paid has no window at all.
	pendingID := fx.pendingBooking(t)
	_, err = fx.svc.RecordRefund(ctx, pendingID, 10_000, "no payment", &fx.actor)
	const wantUnpaid = "validation error: booking has no paid payment to refund"
	if !errors.Is(err, service.ErrValidation) || err.Error() != wantUnpaid {
		t.Errorf("refund on a pending payment = (%v), want %q", err, wantUnpaid)
	}
	if got := fx.countRefunds(t, pendingID); got != 0 {
		t.Errorf("refused unpaid refund wrote %d rows", got)
	}
}

// pendingBooking seeds a booking whose payment is still pending, so the refund
// path can hit its "nothing to refund" guard against a real row.
func (fx *moneyFixture) pendingBooking(t *testing.T) uuid.UUID {
	t.Helper()
	fx.seq++
	start := time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC).Add(time.Duration(fx.seq) * 2 * time.Hour)
	customer := fx.customer
	bookingID := insertBooking(t, fx.pool, bookingSeed{
		CustomerID: &customer,
		LocationID: fx.loc,
		ServiceID:  fx.service,
		StaffID:    fx.staff,
		StartAt:    start,
		PriceIDR:   100_000,
		Status:     string(domain.BookingPendingPayment),
	})
	insertPayment(t, fx.pool, paymentSeed{
		BookingID: bookingID,
		AmountIDR: 100_000,
		Status:    string(domain.PaymentPending),
	})
	return bookingID
}

// mustLogs returns a booking's audit trail newest-first.
func (fx *moneyFixture) mustLogs(t *testing.T, bookingID uuid.UUID) []domain.BookingLog {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	logs, err := fx.bookings.ListLogs(ctx, bookingID)
	if err != nil {
		t.Fatalf("list logs: %v", err)
	}
	return logs
}

// TestVoidRefundExcludedFromTotals is the admin correction path: voiding keeps
// the row as the trail, RefundedTotal and the monthly report drop it, and a new
// refund up to the full paid amount becomes possible again.
func TestVoidRefundExcludedFromTotals(t *testing.T) {
	fx := newMoneyFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bookingID, paymentID := fx.paidBooking(t, 150_000, time.Now(), "")

	rf, err := fx.svc.RecordRefund(ctx, bookingID, 100_000, "mistyped", &fx.actor)
	if err != nil {
		t.Fatalf("record refund: %v", err)
	}
	// Both sides in the refund's recorded month, so the monthly figures below
	// do not depend on where the wall clock sat when the fixture was seeded.
	month := monthOf(t, rf.CreatedAt)
	fx.pinPaidAt(t, paymentID, rf.CreatedAt)

	// Before the correction the refund really is counted, in its recorded month.
	sum, lines, refunds := fx.month(t, month)
	assertMonth(t, sum, month, 150_000, 100_000, 50_000, 1, 1)
	if len(lines) != 1 || lines[0].BookingID != bookingID {
		t.Fatalf("%s lines = %+v, want the paid booking", month, lines)
	}
	if len(refunds) != 1 || refunds[0].ID != rf.ID {
		t.Fatalf("%s refunds = %+v, want the recorded row", month, refunds)
	}
	if got := fx.refundedTotal(t, bookingID); got != 100_000 {
		t.Fatalf("refunded total before void = %d, want 100000", got)
	}

	// A void needs a reason; a second void is refused.
	err = fx.svc.VoidRefund(ctx, rf.ID, "   ", &fx.actor)
	if !errors.Is(err, service.ErrValidation) || err.Error() != "validation error: void reason is required" {
		t.Errorf("void without a reason = %v, want the reason-required copy", err)
	}
	if err := fx.svc.VoidRefund(ctx, rf.ID, "amount was wrong", &fx.actor); err != nil {
		t.Fatalf("void: %v", err)
	}
	if err := fx.svc.VoidRefund(ctx, rf.ID, "again", &fx.actor); !errors.Is(err, service.ErrValidation) {
		t.Errorf("second void = %v, want ErrValidation", err)
	}

	reloaded, err := fx.reports.RefundByID(ctx, rf.ID)
	if err != nil {
		t.Fatalf("reload refund: %v", err)
	}
	if reloaded.VoidedAt == nil || reloaded.VoidReason != "amount was wrong" {
		t.Errorf("voided row = %+v, want voided_at set with the reason", reloaded)
	}
	if reloaded.VoidedByID == nil || *reloaded.VoidedByID != fx.actor {
		t.Errorf("voider = %v, want the seeded CS account", reloaded.VoidedByID)
	}
	if !hasAction(fx.mustLogs(t, bookingID), "Refund Rp 100000 voided") {
		t.Errorf("void did not write its activity row: %v", logActions(fx.mustLogs(t, bookingID)))
	}

	// The voided row stays visible as the correction trail ...
	history, err := fx.reports.RefundsForBooking(ctx, bookingID)
	if err != nil {
		t.Fatalf("refunds for booking: %v", err)
	}
	if len(history) != 1 || !history[0].IsVoid() {
		t.Fatalf("correction trail = %+v, want the one voided row", history)
	}
	// ... while every total drops it.
	if got := fx.refundedTotal(t, bookingID); got != 0 {
		t.Errorf("refunded total after void = %d, want 0", got)
	}
	sum, _, refunds = fx.month(t, month)
	assertMonth(t, sum, month, 150_000, 0, 150_000, 1, 0)
	assertNoRefunds(t, month, refunds)

	// The window is fully open again: the whole paid amount records cleanly.
	rf2, err := fx.svc.RecordRefund(ctx, bookingID, 150_000, "corrected full refund", &fx.actor)
	if err != nil {
		t.Fatalf("full refund after void: %v", err)
	}
	if got := fx.refundedTotal(t, bookingID); got != 150_000 {
		t.Errorf("refunded total after the full refund = %d, want 150000", got)
	}
	// Re-pin for the same month-determinism reason (a corrected refund is a
	// second now() stamp), then the month nets to zero over one paid line.
	month2 := monthOf(t, rf2.CreatedAt)
	fx.pinPaidAt(t, paymentID, rf2.CreatedAt)
	sum2, _, refunds2 := fx.month(t, month2)
	assertMonth(t, sum2, month2, 150_000, 150_000, 0, 1, 1)
	if len(refunds2) != 1 || refunds2[0].ID != rf2.ID {
		t.Errorf("month after the corrected refund lists %+v, want only the new row", refunds2)
	}
}

// TestCashBasisMonthBoundaries pins the aggregation shape: a payment counts in
// its paid_at month, a refund in the month it was recorded — never in the
// payment's month. Two paid bookings sit in adjacent months so a leak between
// buckets would move money between two non-empty rows.
func TestCashBasisMonthBoundaries(t *testing.T) {
	fx := newMoneyFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Booking A pays first and is refunded now: its paid month is two months
	// before the refund's recorded month, with an empty month between them.
	bookingA, paymentA := fx.paidBooking(t, 150_000, time.Now(), "")
	if _, err := fx.svc.RecordRefund(ctx, bookingA, 75_000, "late refund", &fx.actor); err != nil {
		t.Fatalf("record refund: %v", err)
	}
	history, err := fx.reports.RefundsForBooking(ctx, bookingA)
	if err != nil || len(history) != 1 {
		t.Fatalf("refunds for booking A = (%d rows, %v), want 1", len(history), err)
	}
	rf := history[0]

	refundMonth := monthOf(t, rf.CreatedAt)
	monthA := service.PrevMonth(service.PrevMonth(refundMonth))
	monthB := service.PrevMonth(refundMonth)
	if monthA == "" || monthB == "" || monthA == monthB {
		t.Fatalf("month arithmetic produced %q / %q from %q", monthA, monthB, refundMonth)
	}
	fx.pinPaidAt(t, paymentA, wibMidMonth(t, monthA))
	bookingB, _ := fx.paidBooking(t, 200_000, wibMidMonth(t, monthB), "")

	// The refunded payment's own month keeps its gross and carries no refund.
	sumA, linesA, refundsA := fx.month(t, monthA)
	assertMonth(t, sumA, monthA, 150_000, 0, 150_000, 1, 0)
	assertNoRefunds(t, monthA, refundsA)
	if len(linesA) != 1 || linesA[0].BookingID != bookingA {
		t.Fatalf("%s lines = %+v, want booking A only", monthA, linesA)
	}
	if got := monthOf(t, linesA[0].PaidAt); got != monthA {
		t.Errorf("line paid_at bucket = %s, want %s", got, monthA)
	}
	if sumA.QRISIDR != 150_000 || sumA.CashIDR != 0 {
		t.Errorf("%s method split = qris %d / cash %d, want 150000 / 0", monthA, sumA.QRISIDR, sumA.CashIDR)
	}
	// Per-staff rows follow the same bucket.
	if len(sumA.StaffRows) != 1 {
		t.Fatalf("%s staff rows = %d, want 1", monthA, len(sumA.StaffRows))
	}
	if row := sumA.StaffRows[0]; row.GrossIDR != 150_000 || row.RefundIDR != 0 || row.NetIDR != 150_000 {
		t.Errorf("%s staff row = %+v, want gross 150000 refund 0 net 150000", monthA, row)
	}

	// The adjacent month holds the second payment and nothing of A's refund.
	sumB, linesB, refundsB := fx.month(t, monthB)
	assertMonth(t, sumB, monthB, 200_000, 0, 200_000, 1, 0)
	assertNoRefunds(t, monthB, refundsB)
	if len(linesB) != 1 || linesB[0].BookingID != bookingB {
		t.Fatalf("%s lines = %+v, want booking B only", monthB, linesB)
	}

	// The refund lands in the month it was recorded: negative net, no gross.
	sumR, linesR, refundsR := fx.month(t, refundMonth)
	assertMonth(t, sumR, refundMonth, 0, 75_000, -75_000, 0, 1)
	if len(linesR) != 0 {
		t.Errorf("%s lines = %+v, want none", refundMonth, linesR)
	}
	if len(refundsR) != 1 || refundsR[0].BookingID != bookingA {
		t.Fatalf("%s refunds = %+v, want booking A's refund", refundMonth, refundsR)
	}
	if got := monthOf(t, refundsR[0].CreatedAt); got != refundMonth {
		t.Errorf("refund bucket = %s, want %s", got, refundMonth)
	}
	if len(sumR.StaffRows) != 1 || sumR.StaffRows[0].RefundIDR != 75_000 || sumR.StaffRows[0].NetIDR != -75_000 {
		t.Errorf("%s staff rows = %+v, want a refund-only row at -75000", refundMonth, sumR.StaffRows)
	}

	// The unfiltered (all-branches) read returns the same buckets.
	allA, _, _, err := fx.svc.MonthlyReport(ctx, monthA, uuid.Nil)
	if err != nil {
		t.Fatalf("all-branch report %s: %v", monthA, err)
	}
	if allA.GrossIDR != 150_000 || allA.RefundIDR != 0 {
		t.Errorf("all-branch %s = gross %d refund %d, want 150000 / 0", monthA, allA.GrossIDR, allA.RefundIDR)
	}

	// The month dropdown is built from the same buckets.
	months, err := fx.svc.AvailableMonths(ctx, fx.loc)
	if err != nil {
		t.Fatalf("available months: %v", err)
	}
	for _, want := range []string{monthA, monthB, refundMonth} {
		if !containsString(months, want) {
			t.Errorf("available months %v missing %s", months, want)
		}
	}
}

// TestCancelledPaidCountsUntilRefunded pins the cash-basis rule for cancellations:
// a cancelled booking whose money was received still counts until the refund is
// recorded, and that refund then nets the month to zero. Cash basis is not
// status basis.
func TestCancelledPaidCountsUntilRefunded(t *testing.T) {
	fx := newMoneyFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	paidAt := time.Now()
	bookingID, paymentID := fx.paidBooking(t, 150_000, paidAt, domain.BookingCancelled)

	// Cancelled but paid: still revenue, and the line reports its real status.
	paidMonth := monthOf(t, paidAt)
	sum, lines, refunds := fx.month(t, paidMonth)
	assertMonth(t, sum, paidMonth, 150_000, 0, 150_000, 1, 0)
	assertNoRefunds(t, paidMonth, refunds)
	if len(lines) != 1 {
		t.Fatalf("%s lines = %d, want the cancelled-but-paid booking", paidMonth, len(lines))
	}
	if lines[0].Status != domain.BookingCancelled || lines[0].BookingID != bookingID {
		t.Errorf("%s line = %+v, want booking %s with status cancelled", paidMonth, lines[0], bookingID)
	}

	// Once refunded, the money is deducted in the recorded month. Pinning the
	// payment to that same month makes the net-zero reading deterministic
	// (ReportService stamps refunds at now() only).
	rf, err := fx.svc.RecordRefund(ctx, bookingID, 150_000, "cancelled after payment", &fx.actor)
	if err != nil {
		t.Fatalf("refund the cancelled booking: %v", err)
	}
	fx.pinPaidAt(t, paymentID, rf.CreatedAt)
	settledMonth := monthOf(t, rf.CreatedAt)

	settled, lines, refunds := fx.month(t, settledMonth)
	assertMonth(t, settled, settledMonth, 150_000, 150_000, 0, 1, 1)
	if len(lines) != 1 || lines[0].BookingID != bookingID {
		t.Errorf("%s lines = %+v, want the cancelled-but-paid booking", settledMonth, lines)
	}
	if len(refunds) != 1 || refunds[0].BookingID != bookingID {
		t.Fatalf("%s refunds = %+v, want the booking's refund", settledMonth, refunds)
	}
	if len(settled.StaffRows) != 1 {
		t.Fatalf("%s staff rows = %d, want 1", settledMonth, len(settled.StaffRows))
	}
	if row := settled.StaffRows[0]; row.GrossIDR != 150_000 || row.RefundIDR != 150_000 || row.NetIDR != 0 {
		t.Errorf("%s staff row = %+v, want gross 150000 refund 150000 net 0", settledMonth, row)
	}
	if got := fx.refundedTotal(t, bookingID); got != 150_000 {
		t.Errorf("refunded total = %d, want the full 150000", got)
	}
}

// containsString is a tiny membership check for month lists.
func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
