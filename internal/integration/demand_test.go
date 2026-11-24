//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"spa-booking/internal/domain"
)

// The demand block's whole point is that two metric groups use two different
// dates: the funnel (bookings made, cancelled, no-shows) buckets by created_at,
// the visit mix (source, homecare share, promo, transport) buckets by start_at,
// and paid figures by paid_at. A unit test cannot prove which column the SQL
// actually grouped on, so this group seeds one month boundary — a booking
// created on January's last evening for a February visit — and asserts the row
// lands in each group exactly once, in the group's own month.

// pinBookingCreated rewrites created_at, the one column bookingSeed does not
// expose. The demand funnel is defined by the date the booking row was made, so
// the boundary fixture owns that timestamp explicitly instead of inheriting
// now() from the insert.
func pinBookingCreated(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, at time.Time) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, `UPDATE bookings SET created_at=$2 WHERE id=$1`, id, at); err != nil {
		t.Fatalf("pin booking created_at: %v", err)
	}
}

// setTransportFee writes the homecare transport snapshot (the distance band's
// fee). BookingSeed carries the promo discount but not this column.
func setTransportFee(t *testing.T, pool *pgxpool.Pool, id uuid.UUID, feeIDR int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, `UPDATE bookings SET distance_fee_idr=$2 WHERE id=$1`, id, feeIDR); err != nil {
		t.Fatalf("set transport fee: %v", err)
	}
}

// TestDemandReportMonthBoundary seeds a January/February pair plus out-of-window
// rows and asserts the whole ReportDemand struct for both months. The boundary
// booking (created 31 Jan 23:30 WIB, visited 3 Feb) must count in January's
// funnel and February's mix — and nowhere else.
func TestDemandReportMonthBoundary(t *testing.T) {
	fx := newMoneyFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cust := fx.customer

	// A. The boundary row. Created on January's last evening, visit in
	// February, paid on the same January evening.
	createdA := time.Date(2026, 1, 31, 23, 30, 0, 0, wib)
	startA := time.Date(2026, 2, 3, 10, 0, 0, 0, wib)
	a := insertBooking(t, fx.pool, bookingSeed{
		CustomerID: &cust, LocationID: fx.loc, ServiceID: fx.service, StaffID: fx.staff,
		StartAt: startA, PriceIDR: 200_000, Status: string(domain.BookingCompleted),
		Source: "whatsapp", Place: string(domain.PlaceHomecare), VisitAddress: "Jl. Mawar 1",
		DiscountIDR: 20_000,
	})
	pinBookingCreated(t, fx.pool, a, createdA)
	setTransportFee(t, fx.pool, a, 15_000)
	paidA := time.Date(2026, 1, 31, 23, 45, 0, 0, wib)
	insertPayment(t, fx.pool, paymentSeed{BookingID: a, AmountIDR: 200_000, PaidAt: &paidA})

	// B. Created in December, visited and paid in January. Proves the funnel
	// does not pick up a January visit, while the mix and the money do.
	createdB := time.Date(2025, 12, 20, 10, 0, 0, 0, wib)
	startB := time.Date(2026, 1, 10, 9, 0, 0, 0, wib)
	b := insertBooking(t, fx.pool, bookingSeed{
		CustomerID: &cust, LocationID: fx.loc, ServiceID: fx.service, StaffID: fx.staff,
		StartAt: startB, PriceIDR: 150_000, Status: string(domain.BookingCompleted),
		Source: "web",
	})
	pinBookingCreated(t, fx.pool, b, createdB)
	paidB := time.Date(2026, 1, 12, 10, 0, 0, 0, wib)
	insertPayment(t, fx.pool, paymentSeed{BookingID: b, AmountIDR: 150_000, PaidAt: &paidB})

	// C. January created, January visited, no-show: the funnel's no-show and
	// the rate's numerator, plus a walk-in in the mix.
	createdC := time.Date(2026, 1, 5, 9, 0, 0, 0, wib)
	startC := time.Date(2026, 1, 20, 11, 0, 0, 0, wib)
	c := insertBooking(t, fx.pool, bookingSeed{
		CustomerID: &cust, LocationID: fx.loc, ServiceID: fx.service, StaffID: fx.staff,
		StartAt: startC, PriceIDR: 150_000, Status: string(domain.BookingNoShow),
		Source: "walkin",
	})
	pinBookingCreated(t, fx.pool, c, createdC)

	// D. January created and visited, cancelled by phone, carrying a promo
	// discount. The cancellation is a funnel fact; the discount is a visit fact.
	createdD := time.Date(2026, 1, 8, 9, 0, 0, 0, wib)
	startD := time.Date(2026, 1, 25, 13, 0, 0, 0, wib)
	d := insertBooking(t, fx.pool, bookingSeed{
		CustomerID: &cust, LocationID: fx.loc, ServiceID: fx.service, StaffID: fx.staff,
		StartAt: startD, PriceIDR: 150_000, Status: string(domain.BookingCancelled),
		Source: "phone", DiscountIDR: 5_000,
	})
	pinBookingCreated(t, fx.pool, d, createdD)

	// E. Created 00:30 WIB on 1 February — 17:30 UTC on 31 January — and paid
	// 00:45 WIB that same night. A naive UTC read puts both in January; only
	// the Asia/Jakarta bucket puts them in February. This is the WIB premise.
	createdE := time.Date(2026, 2, 1, 0, 30, 0, 0, wib)
	startE := time.Date(2026, 2, 5, 10, 0, 0, 0, wib)
	e := insertBooking(t, fx.pool, bookingSeed{
		CustomerID: &cust, LocationID: fx.loc, ServiceID: fx.service, StaffID: fx.staff,
		StartAt: startE, PriceIDR: 100_000, Status: string(domain.BookingConfirmed),
		Source: "web",
	})
	pinBookingCreated(t, fx.pool, e, createdE)
	paidE := time.Date(2026, 2, 1, 0, 45, 0, 0, wib)
	insertPayment(t, fx.pool, paymentSeed{BookingID: e, AmountIDR: 100_000, PaidAt: &paidE})

	// F. December only: must never appear in either month.
	createdF := time.Date(2025, 12, 1, 9, 0, 0, 0, wib)
	startF := time.Date(2025, 12, 15, 9, 0, 0, 0, wib)
	f := insertBooking(t, fx.pool, bookingSeed{
		CustomerID: &cust, LocationID: fx.loc, ServiceID: fx.service, StaffID: fx.staff,
		StartAt: startF, PriceIDR: 50_000, Status: string(domain.BookingCompleted),
		Source: "web",
	})
	pinBookingCreated(t, fx.pool, f, createdF)
	paidF := time.Date(2025, 12, 16, 9, 0, 0, 0, wib)
	insertPayment(t, fx.pool, paymentSeed{BookingID: f, AmountIDR: 50_000, PaidAt: &paidF})

	// G. Another branch's January booking: the location filter must drop it.
	otherLoc := insertLocation(t, fx.pool, "Other Branch")
	otherSvc := insertService(t, fx.pool, otherLoc, "Other Treatment", 90_000)
	otherStf := insertStaff(t, fx.pool, otherLoc, "Other Therapist")
	createdG := time.Date(2026, 1, 9, 9, 0, 0, 0, wib)
	startG := time.Date(2026, 1, 11, 9, 0, 0, 0, wib)
	g := insertBooking(t, fx.pool, bookingSeed{
		CustomerID: &cust, LocationID: otherLoc, ServiceID: otherSvc, StaffID: otherStf,
		StartAt: startG, PriceIDR: 90_000, Status: string(domain.BookingCompleted),
		Source: "walkin",
	})
	pinBookingCreated(t, fx.pool, g, createdG)

	// Prove the WIB premise before trusting the February assertions.
	if got := createdE.UTC().Format("2006-01-02T15:04Z"); got != "2026-01-31T17:30Z" {
		t.Fatalf("fixture premise broken: createdE UTC = %s, want 2026-01-31T17:30Z", got)
	}
	if got := paidE.UTC().Format("2006-01-02T15:04Z"); got != "2026-01-31T17:45Z" {
		t.Fatalf("fixture premise broken: paidE UTC = %s, want 2026-01-31T17:45Z", got)
	}

	// January: funnel from A, C, D; mix from B, C, D (A's visit is February);
	// paid from A and B. GrossPaid comes from the same summary the page pins,
	// never re-derived by the demand query.
	janSum, _, _ := fx.month(t, "2026-01")
	if janSum.GrossIDR != 350_000 {
		t.Fatalf("January summary gross = %d, want 350000", janSum.GrossIDR)
	}
	jan, err := fx.svc.DemandReport(ctx, "2026-01", fx.loc, janSum.GrossIDR)
	if err != nil {
		t.Fatalf("January demand: %v", err)
	}
	if jan.GrossPaid != 350_000 {
		t.Errorf("January GrossPaid = %d, want the summary's 350000", jan.GrossPaid)
	}
	if jan.BookingsCreated != 3 || jan.Cancelled != 1 || jan.NoShows != 1 {
		t.Errorf("January funnel = made %d / cancelled %d / no-shows %d, want 3/1/1",
			jan.BookingsCreated, jan.Cancelled, jan.NoShows)
	}
	if jan.BookingsPaid != 2 {
		t.Errorf("January paid bookings = %d, want 2 (A and B paid in January)", jan.BookingsPaid)
	}
	if jan.Visits != 3 || jan.SourceWeb != 1 || jan.SourceWalkin != 1 ||
		jan.SourceWhatsapp != 0 || jan.SourcePhone != 1 {
		t.Errorf("January mix = visits %d / web %d / walkin %d / whatsapp %d / phone %d, want 3/1/1/0/1",
			jan.Visits, jan.SourceWeb, jan.SourceWalkin, jan.SourceWhatsapp, jan.SourcePhone)
	}
	if jan.HomecareVisits != 0 || jan.HomecareShareBp != 0 {
		t.Errorf("January homecare = %d visits / %d bp, want 0/0", jan.HomecareVisits, jan.HomecareShareBp)
	}
	if jan.PromoDiscIDR != 5_000 || jan.TransportIDR != 0 {
		t.Errorf("January promo/transport = %d/%d, want 5000/0", jan.PromoDiscIDR, jan.TransportIDR)
	}
	if jan.TerminalVisits != 3 || jan.NoShowVisits != 1 {
		t.Errorf("January rate basis = %d no-shows of %d finished visits, want 1/3",
			jan.NoShowVisits, jan.TerminalVisits)
	}
	if jan.NoShowRateBp != 3333 {
		t.Errorf("January no-show rate = %d bp, want 3333 (1 of 3)", jan.NoShowRateBp)
	}

	// February: the boundary row's visit lands here (whatsapp/homecare/promo/
	// transport), its creation and payment stay in January.
	febSum, _, _ := fx.month(t, "2026-02")
	if febSum.GrossIDR != 100_000 {
		t.Fatalf("February summary gross = %d, want 100000", febSum.GrossIDR)
	}
	feb, err := fx.svc.DemandReport(ctx, "2026-02", fx.loc, febSum.GrossIDR)
	if err != nil {
		t.Fatalf("February demand: %v", err)
	}
	if feb.BookingsCreated != 1 || feb.Cancelled != 0 || feb.NoShows != 0 {
		t.Errorf("February funnel = made %d / cancelled %d / no-shows %d, want 1/0/0",
			feb.BookingsCreated, feb.Cancelled, feb.NoShows)
	}
	if feb.BookingsPaid != 1 {
		t.Errorf("February paid bookings = %d, want 1 (E paid 00:45 WIB on the 1st)", feb.BookingsPaid)
	}
	if feb.Visits != 2 || feb.SourceWeb != 1 || feb.SourceWhatsapp != 1 {
		t.Errorf("February mix = visits %d / web %d / whatsapp %d, want 2/1/1",
			feb.Visits, feb.SourceWeb, feb.SourceWhatsapp)
	}
	if feb.HomecareVisits != 1 || feb.HomecareShareBp != 5000 {
		t.Errorf("February homecare = %d visits / %d bp, want 1/5000", feb.HomecareVisits, feb.HomecareShareBp)
	}
	if feb.PromoDiscIDR != 20_000 || feb.TransportIDR != 15_000 {
		t.Errorf("February promo/transport = %d/%d, want 20000/15000",
			feb.PromoDiscIDR, feb.TransportIDR)
	}
	if feb.TerminalVisits != 1 || feb.NoShowVisits != 0 || feb.NoShowRateBp != 0 {
		t.Errorf("February rate = %d no-shows of %d finished visits, %d bp, want 0/1/0",
			feb.NoShowVisits, feb.TerminalVisits, feb.NoShowRateBp)
	}

	// Exactly once: each seeded row is counted in exactly one month per group.
	if got := jan.BookingsCreated + feb.BookingsCreated; got != 4 {
		t.Errorf("created Jan+Feb = %d, want 4 (A, C, D, E each once)", got)
	}
	if got := jan.Visits + feb.Visits; got != 5 {
		t.Errorf("visits Jan+Feb = %d, want 5 (A-E each once)", got)
	}
	if got := jan.BookingsPaid + feb.BookingsPaid; got != 3 {
		t.Errorf("paid Jan+Feb = %d, want 3 (A, B, E each once)", got)
	}
	if got := jan.PromoDiscIDR + feb.PromoDiscIDR; got != 25_000 {
		t.Errorf("promo Jan+Feb = %d, want 25000", got)
	}
	if got := jan.TransportIDR + feb.TransportIDR; got != 15_000 {
		t.Errorf("transport Jan+Feb = %d, want 15000", got)
	}

	// An empty month is explicit zeros, not an error and not a blank rate.
	empty, err := fx.svc.DemandReport(ctx, "2026-05", fx.loc, 0)
	if err != nil {
		t.Fatalf("empty-month demand: %v", err)
	}
	if empty != (domain.ReportDemand{}) {
		t.Errorf("empty month = %+v, want the zero struct", empty)
	}
}
