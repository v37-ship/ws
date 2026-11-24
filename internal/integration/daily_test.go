//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"spa-booking/internal/domain"
)

// The daily view's four counts and its cash/QRIS split are SQL facts a unit
// test cannot reach: the visit mix groups the day's bookings by status, and the
// money is bucketed by paid_at in Asia/Jakarta — never by the booking's date.
// This group seeds one operating day and executes the real repo/service reads
// over real rows, so a dropped AT TIME ZONE, a wrong status literal or a
// location filter that leaks between branches fails here.

// dailyDay is a fixed past WIB day: staying in the past keeps the "upcoming"
// window deterministic without depending on when the suite runs.
const dailyDay = "2020-01-02"

// dailyFixture is the seeded day plus the branch-scoping controls.
type dailyFixture struct {
	*moneyFixture
	otherLoc uuid.UUID
	otherSvc uuid.UUID
	otherStf uuid.UUID
}

// newDailyFixture seeds the shared report fixture (one branch, treatment,
// therapist, customer) and a second branch with its own rows so the location
// filter is provable.
func newDailyFixture(t *testing.T) *dailyFixture {
	t.Helper()
	fx := &dailyFixture{moneyFixture: newMoneyFixture(t)}
	fx.otherLoc = insertLocation(t, fx.pool, "Other Branch")
	fx.otherSvc = insertService(t, fx.pool, fx.otherLoc, "Other Treatment", 90_000)
	fx.otherStf = insertStaff(t, fx.pool, fx.otherLoc, "Other Therapist")
	return fx
}

// seedVisit writes one booking at the given WIB hour on dailyDay.
func (fx *dailyFixture) seedVisit(t *testing.T, loc, svc, staff uuid.UUID, hour, min int, status domain.BookingStatus, source string) uuid.UUID {
	t.Helper()
	if source == "" {
		source = "web"
	}
	start := time.Date(2020, 1, 2, hour, min, 0, 0, wib)
	customer := fx.customer
	return insertBooking(t, fx.pool, bookingSeed{
		CustomerID: &customer,
		LocationID: loc,
		ServiceID:  svc,
		StaffID:    staff,
		StartAt:    start,
		PriceIDR:   150_000,
		Status:     string(status),
		Source:     source,
	})
}

// TestDailyReportSeededDay asserts the whole daily read over a day holding one
// of every status, a walk-in, both payment methods on either side of the WIB
// midnight boundary, and rows that belong to the next day or the other branch.
func TestDailyReportSeededDay(t *testing.T) {
	fx := newDailyFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// The day's visit mix at the primary branch. Two completed rows (one of
	// them a walk-in), a checked-in row that is neither completed nor
	// cancelled, one no-show and two cancellations.
	completedWeb := fx.seedVisit(t, fx.loc, fx.service, fx.staff, 8, 0, domain.BookingCompleted, "")
	fx.seedVisit(t, fx.loc, fx.service, fx.staff, 9, 0, domain.BookingCompleted, "walkin")
	fx.seedVisit(t, fx.loc, fx.service, fx.staff, 10, 0, domain.BookingCheckedIn, "")
	fx.seedVisit(t, fx.loc, fx.service, fx.staff, 12, 0, domain.BookingNoShow, "")
	fx.seedVisit(t, fx.loc, fx.service, fx.staff, 13, 0, domain.BookingCancelled, "")
	fx.seedVisit(t, fx.loc, fx.service, fx.staff, 14, 0, domain.BookingCancelled, "")

	// Upcoming windows: 11:00 is already past at noon, 15:00 and 16:30 are
	// still ahead, and the pending row is ahead but not confirmed.
	fx.seedVisit(t, fx.loc, fx.service, fx.staff, 11, 0, domain.BookingConfirmed, "")
	up15 := fx.seedVisit(t, fx.loc, fx.service, fx.staff, 15, 0, domain.BookingConfirmed, "")
	fx.seedVisit(t, fx.loc, fx.service, fx.staff, 16, 30, domain.BookingConfirmed, "")
	fx.seedVisit(t, fx.loc, fx.service, fx.staff, 17, 30, domain.BookingPendingPayment, "")

	// A confirmed row the next day is outside the day filter entirely.
	nextDay := time.Date(2020, 1, 3, 9, 0, 0, 0, wib)
	customer := fx.customer
	nextID := insertBooking(t, fx.pool, bookingSeed{
		CustomerID: &customer, LocationID: fx.loc, ServiceID: fx.service,
		StaffID: fx.staff, StartAt: nextDay, PriceIDR: 150_000,
		Status: string(domain.BookingConfirmed),
	})

	// Money by paid_at WIB day. The cash row is 23:30 WIB on the 2nd (already
	// the 3rd in UTC+... no: 16:30 UTC on the 2nd); the QRIS row is 00:30 WIB
	// on the 2nd, which is 17:30 UTC on the *1st*. Both only land on the 2nd
	// when the query converts to Asia/Jakarta. The 40.000 QRIS row is 00:30
	// WIB on the 3rd (17:30 UTC on the 2nd) and must stay out.
	cashAt := time.Date(2020, 1, 2, 23, 30, 0, 0, wib)
	insertPayment(t, fx.pool, paymentSeed{
		BookingID: completedWeb, AmountIDR: 100_000,
		Method: string(domain.PaymentMethodCash), PaidAt: &cashAt,
	})
	qrisAt := time.Date(2020, 1, 2, 0, 30, 0, 0, wib)
	insertPayment(t, fx.pool, paymentSeed{
		BookingID: up15, AmountIDR: 75_000,
		Method: string(domain.PaymentMethodQRIS), PaidAt: &qrisAt,
	})
	lateAt := time.Date(2020, 1, 3, 0, 30, 0, 0, wib)
	insertPayment(t, fx.pool, paymentSeed{
		BookingID: nextID, AmountIDR: 40_000,
		Method: string(domain.PaymentMethodQRIS), PaidAt: &lateAt,
	})

	// The other branch's own visit and payment on the same day.
	otherVisit := fx.seedVisit(t, fx.otherLoc, fx.otherSvc, fx.otherStf, 9, 0, domain.BookingCompleted, "")
	otherPaid := time.Date(2020, 1, 2, 12, 0, 0, 0, wib)
	insertPayment(t, fx.pool, paymentSeed{
		BookingID: otherVisit, AmountIDR: 500_000,
		Method: string(domain.PaymentMethodCash), PaidAt: &otherPaid,
	})

	// Prove the WIB boundary premise in UTC before trusting the assertions.
	if got := cashAt.UTC().Format(time.RFC3339); got != "2020-01-02T16:30:00Z" {
		t.Fatalf("fixture premise broken: cash paid_at UTC = %s, want 2020-01-02T16:30:00Z", got)
	}
	if got := qrisAt.UTC().Format(time.RFC3339); got != "2020-01-01T17:30:00Z" {
		t.Fatalf("fixture premise broken: qris paid_at UTC = %s, want 2020-01-01T17:30:00Z", got)
	}

	// The service's daily read: counts and money for the primary branch.
	sum, err := fx.svc.DailyReport(ctx, dailyDay, fx.loc)
	if err != nil {
		t.Fatalf("daily report: %v", err)
	}
	if sum.Date != dailyDay {
		t.Errorf("summary date = %q, want %s", sum.Date, dailyDay)
	}
	if sum.Completed != 2 || sum.WalkIns != 1 || sum.NoShows != 1 || sum.Cancelled != 2 {
		t.Errorf("visit mix = completed %d / walk-ins %d / no-shows %d / cancelled %d, want 2/1/1/2",
			sum.Completed, sum.WalkIns, sum.NoShows, sum.Cancelled)
	}
	if sum.CashIDR != 100_000 || sum.QRISIDR != 75_000 {
		t.Errorf("money = cash %d / qris %d, want 100000 / 75000 (paid_at bucketed in WIB)",
			sum.CashIDR, sum.QRISIDR)
	}
	// The fixed day is long past, so the service's upcoming window is empty.
	if len(sum.Upcoming) != 0 {
		t.Errorf("upcoming for a past day = %d rows, want 0: %+v", len(sum.Upcoming), sum.Upcoming)
	}

	// Branch scoping: all-branches reads add the other branch's row and money
	// but still keep the next-day payment out.
	all, err := fx.svc.DailyReport(ctx, dailyDay, uuid.Nil)
	if err != nil {
		t.Fatalf("all-branch daily report: %v", err)
	}
	if all.Completed != 3 {
		t.Errorf("all-branch completed = %d, want 3", all.Completed)
	}
	if all.CashIDR != 600_000 || all.QRISIDR != 75_000 {
		t.Errorf("all-branch money = cash %d / qris %d, want 600000 / 75000", all.CashIDR, all.QRISIDR)
	}

	// Upcoming with a pinned clock: only the confirmed rows still ahead at noon,
	// in start order, with their display fields. The 11:00 confirmed row, the
	// 17:30 pending row and the next day's row are all excluded.
	noon := time.Date(2020, 1, 2, 12, 0, 0, 0, wib)
	upcoming, err := fx.reports.DailyUpcoming(ctx, dailyDay, noon, fx.loc)
	if err != nil {
		t.Fatalf("daily upcoming: %v", err)
	}
	if len(upcoming) != 2 {
		t.Fatalf("upcoming = %d rows, want 2: %+v", len(upcoming), upcoming)
	}
	if got := upcoming[0].StartAt.In(wib).Format("15:04"); got != "15:00" {
		t.Errorf("first upcoming slot = %s, want 15:00", got)
	}
	if got := upcoming[1].StartAt.In(wib).Format("15:04"); got != "16:30" {
		t.Errorf("second upcoming slot = %s, want 16:30", got)
	}
	first := upcoming[0]
	if first.BookingID != up15 {
		t.Errorf("first upcoming booking = %s, want the 15:00 row %s", first.BookingID, up15)
	}
	if first.Status != domain.BookingConfirmed {
		t.Errorf("first upcoming status = %q, want confirmed", first.Status)
	}
	if first.CustomerName == "" || first.ServiceName != "Report Treatment" || first.StaffName != "Report Therapist" {
		t.Errorf("first upcoming display fields = customer %q / service %q / staff %q, want the seeded names",
			first.CustomerName, first.ServiceName, first.StaffName)
	}

	// At 17:00 the 16:30 row has started, so the window shrinks — the filter is
	// a real start_at comparison, not "all confirmed rows of the day".
	evening := time.Date(2020, 1, 2, 17, 0, 0, 0, wib)
	left, err := fx.reports.DailyUpcoming(ctx, dailyDay, evening, fx.loc)
	if err != nil {
		t.Fatalf("daily upcoming at 17:00: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("upcoming at 17:00 = %d rows, want 0: %+v", len(left), left)
	}
}
