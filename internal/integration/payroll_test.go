//go:build integration

package integration

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"
	"spa-booking/internal/service"
)

// Payroll is accrue-on-service-date with refunds deducted by recorded date, and
// the three things a unit test cannot prove are the SQL window's WIB dates, the
// status/staff filters, and the money columns the joins read. This group seeds
// real rows around the boundaries that would drift:
//
//   - a visit on Sunday 23:30 WIB and one on Monday 00:05 WIB (Sunday evening
//     UTC) must land in different WIB weeks;
//   - a visit on the month's last day at 23:30 WIB belongs to that month;
//   - a confirmed booking, a visit in another month and a voided refund never
//     reach a row;
//   - a paused therapist with completed visits still appears, and a therapist
//     with no rate row appears flagged rather than omitted;
//   - a refund recorded in December on a November visit shows up in December as
//     a negative line, and re-reading November is unchanged;
//   - a weekly-cadence fixed is paid once per contained WIB week on the month
//     view (November 2026 has five Mondays) and once on the week view.

// seedPayrollRefund writes one refund with an explicit created_at and returns
// its id. created_by_id is left NULL: the payroll reads never join the maker.
func seedPayrollRefund(t *testing.T, pool *pgxpool.Pool, ctx context.Context, bookingID uuid.UUID, amount int64, at time.Time, reason string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO refunds (id, booking_id, amount_idr, reason, created_at)
		 VALUES ($1,$2,$3,$4,$5)`,
		id, bookingID, amount, reason, at,
	); err != nil {
		t.Fatalf("insert payroll refund at %s: %v", at.Format(time.RFC3339), err)
	}
	return id
}

func payrollReport(t *testing.T, svc *service.PayrollService, kind string, start time.Time) service.PayrollReport {
	t.Helper()
	rep, err := svc.Report(context.Background(), kind, start)
	if err != nil {
		t.Fatalf("payroll report %s from %s: %v", kind, start.Format(time.RFC3339), err)
	}
	return rep
}

func payrollRowNamed(t *testing.T, rep service.PayrollReport, name string) service.PayrollRow {
	t.Helper()
	for _, row := range rep.Rows {
		if row.StaffName == name {
			return row
		}
	}
	names := ""
	for i, row := range rep.Rows {
		if i > 0 {
			names += ", "
		}
		names += row.StaffName
	}
	t.Fatalf("report has no row for %q; rows: %s", name, names)
	return service.PayrollRow{}
}

func TestPayrollReportOverSQL(t *testing.T) {
	pool := OpenTestDB(t)
	TruncateAll(t, pool)
	ctx := context.Background()

	loc := insertLocation(t, pool, "Payroll Branch")
	treatment := insertService(t, pool, loc, "Payroll Treatment", 150_000)
	customer := insertUser(t, pool, "payroll-customer@example.com", string(domain.RoleCustomer))

	ayu := insertStaff(t, pool, loc, "Ayu")
	bima := insertStaff(t, pool, loc, "Bima")
	citra := insertStaff(t, pool, loc, "Citra")
	dewi := insertStaff(t, pool, loc, "Dewi")
	// A paused therapist keeps her historical visits: payroll must still pay
	// them, so active is deliberately not a filter anywhere.
	if _, err := pool.Exec(ctx, `UPDATE staff SET active = false WHERE id = $1`, citra); err != nil {
		t.Fatalf("pause Citra: %v", err)
	}

	comps := repo.NewStaffCompRepo(pool)
	createComp := func(staffID uuid.UUID, bp int, fixed int64, cadence, base string) {
		t.Helper()
		if err := comps.Create(ctx, &domain.StaffComp{
			StaffID:        staffID,
			PercentBp:      bp,
			FixedIDR:       fixed,
			Cadence:        cadence,
			CommissionBase: base,
			EffectiveFrom:  payrollDate(2026, time.January, 1),
		}); err != nil {
			t.Fatalf("create payroll rate: %v", err)
		}
	}
	createComp(ayu, 2500, 500_000, domain.CompCadenceMonthly, domain.CompBaseNet)
	createComp(bima, 2000, 300_000, domain.CompCadenceWeekly, domain.CompBaseList)
	createComp(citra, 1000, 0, domain.CompCadenceMonthly, domain.CompBaseNet)
	// Dewi has completed work and no rate row, on purpose.

	wibAt := func(year int, month time.Month, day, hour, minute int) time.Time {
		return time.Date(year, month, day, hour, minute, 0, 0, wib)
	}
	book := func(staffID uuid.UUID, at time.Time, price, discount int64) uuid.UUID {
		t.Helper()
		return insertBooking(t, pool, bookingSeed{
			CustomerID:  &customer,
			LocationID:  loc,
			ServiceID:   treatment,
			StaffID:     staffID,
			StartAt:     at,
			Status:      string(domain.BookingCompleted),
			PriceIDR:    price,
			DiscountIDR: discount,
		})
	}

	// Ayu: two visits in the week of the 23rd, one at the last minute of
	// November, one in December just after midnight (Monday 00:05 WIB).
	a1 := book(ayu, wibAt(2026, time.November, 23, 10, 0), 200_000, 50_000)
	a2 := insertBooking(t, pool, bookingSeed{
		CustomerID:   &customer,
		LocationID:   loc,
		ServiceID:    treatment,
		StaffID:      ayu,
		StartAt:      wibAt(2026, time.November, 28, 10, 0),
		Status:       string(domain.BookingCompleted),
		PriceIDR:     175_000,
		Place:        string(domain.PlaceHomecare),
		VisitAddress: "Jl. Uji Homecare 1",
	})
	// Homecare transport is logistics, never commission: the fee is seeded so a
	// gross that included it would visibly overshoot the assertion below.
	if _, err := pool.Exec(ctx,
		`UPDATE bookings SET distance_km = 4.0, distance_fee_idr = 25_000 WHERE id = $1`, a2); err != nil {
		t.Fatalf("seed homecare transport fee: %v", err)
	}
	// The booking fee is the app's own charge, never commission: seeding it on
	// Ayu's first visit makes a gross that included it visibly overshoot.
	if _, err := pool.Exec(ctx,
		`UPDATE bookings SET booking_fee_idr = 15_000 WHERE id = $1`, a1); err != nil {
		t.Fatalf("seed booking fee: %v", err)
	}
	book(ayu, wibAt(2026, time.November, 30, 23, 30), 300_000, 0) // last minute of November
	book(ayu, wibAt(2026, time.December, 1, 0, 5), 400_000, 0)

	// Bima: a November visit, an October visit (other period), a confirmed
	// booking (wrong status), and a Monday 00:05 WIB visit that a UTC week
	// bucket would file under the previous Sunday.
	b1 := book(bima, wibAt(2026, time.November, 24, 10, 0), 150_000, 0)
	book(bima, wibAt(2026, time.October, 30, 10, 0), 200_000, 0)
	insertBooking(t, pool, bookingSeed{
		CustomerID: &customer,
		LocationID: loc,
		ServiceID:  treatment,
		StaffID:    bima,
		StartAt:    wibAt(2026, time.November, 27, 10, 0),
		Status:     string(domain.BookingConfirmed),
		PriceIDR:   999_000,
	})
	book(bima, wibAt(2026, time.November, 30, 0, 5), 50_000, 0)

	// A cancelled, paid-then-refunded booking: the visit never happened, so the
	// refund is a logistics reversal and must not touch payroll (R17).
	cancelled := insertBooking(t, pool, bookingSeed{
		CustomerID: &customer,
		LocationID: loc,
		ServiceID:  treatment,
		StaffID:    bima,
		StartAt:    wibAt(2026, time.December, 3, 10, 0),
		Status:     string(domain.BookingCancelled),
		PriceIDR:   250_000,
	})

	// Citra (paused): Thursday and Sunday 23:30 WIB — the Sunday visit must stay
	// in its own week.
	book(citra, wibAt(2026, time.November, 26, 10, 0), 100_000, 20_000)
	book(citra, wibAt(2026, time.November, 29, 23, 30), 100_000, 0)

	// Dewi: no rate row at all, plus a refund in the period so a refund still has
	// to resolve a base for her.
	d1 := book(dewi, wibAt(2026, time.November, 25, 10, 0), 180_000, 30_000)

	// Ayu's November booking is over-refunded on purpose: the cap is the
	// booking's net base (150,000), so only 150,000 of this 400,000 deducts.
	seedPayrollRefund(t, pool, ctx, a1, 400_000, wibAt(2026, time.November, 26, 12, 0), "capped refund")
	// Bima: 60,000 deducted in November, a late 120,000 recorded in December on
	// the same completed November booking (a performed visit still deducts in
	// its refund's period), and a voided refund that is not money at all.
	seedPayrollRefund(t, pool, ctx, b1, 60_000, wibAt(2026, time.November, 25, 12, 0), "partial refund")
	seedPayrollRefund(t, pool, ctx, b1, 120_000, wibAt(2026, time.December, 2, 12, 0), "late refund")
	voided := seedPayrollRefund(t, pool, ctx, b1, 999_000, wibAt(2026, time.November, 1, 12, 0), "voided refund")
	if _, err := pool.Exec(ctx, `UPDATE refunds SET voided_at = now() WHERE id = $1`, voided); err != nil {
		t.Fatalf("void seeded refund: %v", err)
	}
	// The first must never reach payroll (cancelled visit); the second is over
	// Dewi's net cap (150,000) and proves the missing rate's fallback base is
	// net, not list (which would deduct all 170,000).
	seedPayrollRefund(t, pool, ctx, cancelled, 250_000, wibAt(2026, time.December, 4, 12, 0), "cancelled visit refund")
	seedPayrollRefund(t, pool, ctx, d1, 170_000, wibAt(2026, time.November, 25, 13, 0), "over-cap refund, no rate")

	svc := service.NewPayrollService(repo.NewPayrollRepo(pool), comps)

	t.Run("November month", func(t *testing.T) {
		rep := payrollReport(t, svc, service.PayrollKindMonth, wibAt(2026, time.November, 15, 0, 0))
		if got := rep.Period.Start.Format("2006-01-02"); got != "2026-11-01" {
			t.Fatalf("period start = %s, want 2026-11-01", got)
		}
		if got := rep.Period.End.Format("2006-01-02"); got != "2026-11-30" {
			t.Fatalf("period end = %s, want 2026-11-30", got)
		}
		if len(rep.Rows) != 4 {
			t.Fatalf("rows = %d, want Ayu, Bima, Citra, Dewi", len(rep.Rows))
		}
		if rep.Rows[0].StaffName != "Ayu" || rep.Rows[1].StaffName != "Bima" ||
			rep.Rows[2].StaffName != "Citra" || rep.Rows[3].StaffName != "Dewi" {
			t.Fatalf("rows are not name-sorted: %+v", rep.Rows)
		}

		ayuRow := payrollRowNamed(t, rep, "Ayu")
		if ayuRow.Sessions != 3 || ayuRow.GrossIDR != 625_000 ||
			ayuRow.RefundIDR != 150_000 || ayuRow.NetIDR != 475_000 {
			t.Errorf("Ayu = sessions %d gross %d refund %d net %d, want 3/625000/150000/475000",
				ayuRow.Sessions, ayuRow.GrossIDR, ayuRow.RefundIDR, ayuRow.NetIDR)
		}
		if ayuRow.PercentBp != 2500 || ayuRow.Base != domain.CompBaseNet || ayuRow.RateMissing {
			t.Errorf("Ayu rate = %d bp, base %q, missing %v; want 2500/net/false",
				ayuRow.PercentBp, ayuRow.Base, ayuRow.RateMissing)
		}
		// 475,000 × 25% = 118,750 → 119,000; monthly fixed once.
		if ayuRow.CommissionIDR != 119_000 || ayuRow.FixedIDR != 500_000 || ayuRow.TotalIDR != 619_000 {
			t.Errorf("Ayu commission/fixed/total = %d/%d/%d, want 119000/500000/619000",
				ayuRow.CommissionIDR, ayuRow.FixedIDR, ayuRow.TotalIDR)
		}
		if len(ayuRow.Visits) != 3 || len(ayuRow.RefundLines) != 1 {
			t.Fatalf("Ayu lines = %d visits / %d refunds, want 3/1",
				len(ayuRow.Visits), len(ayuRow.RefundLines))
		}
		if ayuRow.RefundLines[0].AmountIDR != 400_000 {
			t.Errorf("Ayu refund line = %d, want the recorded 400000 (deduction is capped separately)",
				ayuRow.RefundLines[0].AmountIDR)
		}

		bimaRow := payrollRowNamed(t, rep, "Bima")
		// b1 plus the Monday 00:05 visit; the October and confirmed rows are out.
		if bimaRow.Sessions != 2 || bimaRow.GrossIDR != 200_000 {
			t.Errorf("Bima sessions/gross = %d/%d, want 2/200000", bimaRow.Sessions, bimaRow.GrossIDR)
		}
		if bimaRow.Base != domain.CompBaseList {
			t.Errorf("Bima base = %q, want list (the discount field is zero here anyway)", bimaRow.Base)
		}
		if bimaRow.RefundIDR != 60_000 || bimaRow.NetIDR != 140_000 {
			t.Errorf("Bima refund/net = %d/%d, want 60000/140000 (voided refund excluded)",
				bimaRow.RefundIDR, bimaRow.NetIDR)
		}
		// 140,000 × 20% = 28,000; weekly fixed × five November Mondays.
		if bimaRow.CommissionIDR != 28_000 || bimaRow.FixedIDR != 1_500_000 || bimaRow.TotalIDR != 1_528_000 {
			t.Errorf("Bima commission/fixed/total = %d/%d/%d, want 28000/1500000/1528000",
				bimaRow.CommissionIDR, bimaRow.FixedIDR, bimaRow.TotalIDR)
		}

		citraRow := payrollRowNamed(t, rep, "Citra")
		if citraRow.Sessions != 2 || citraRow.GrossIDR != 180_000 {
			t.Errorf("Citra sessions/gross = %d/%d, want 2/180000 (paused staff still count)",
				citraRow.Sessions, citraRow.GrossIDR)
		}
		if citraRow.CommissionIDR != 18_000 || citraRow.TotalIDR != 18_000 {
			t.Errorf("Citra commission/total = %d/%d, want 18000/18000",
				citraRow.CommissionIDR, citraRow.TotalIDR)
		}

		dewiRow := payrollRowNamed(t, rep, "Dewi")
		if !dewiRow.RateMissing || dewiRow.Base != "" || dewiRow.PercentBp != 0 {
			t.Errorf("Dewi flag/base/bp = %v/%q/%d, want true/empty/0",
				dewiRow.RateMissing, dewiRow.Base, dewiRow.PercentBp)
		}
		// A refund with no rate row still deducts, capped at the net-default
		// base (150,000): the 170,000 refund line stays at 170,000 but only
		// 150,000 comes off. Commission stays zero because no rate is configured.
		if dewiRow.GrossIDR != 150_000 || dewiRow.RefundIDR != 150_000 || dewiRow.NetIDR != 0 {
			t.Errorf("Dewi gross/refund/net = %d/%d/%d, want 150000/150000/0 (net-default cap)",
				dewiRow.GrossIDR, dewiRow.RefundIDR, dewiRow.NetIDR)
		}
		if len(dewiRow.RefundLines) != 1 || dewiRow.RefundLines[0].AmountIDR != 170_000 {
			t.Errorf("Dewi refund lines = %+v, want the recorded 170000 line", dewiRow.RefundLines)
		}
		if dewiRow.CommissionIDR != 0 || dewiRow.FixedIDR != 0 || dewiRow.TotalIDR != 0 {
			t.Errorf("Dewi commission/fixed/total = %d/%d/%d, want 0/0/0",
				dewiRow.CommissionIDR, dewiRow.FixedIDR, dewiRow.TotalIDR)
		}
		if rep.MissingRate != 1 {
			t.Errorf("MissingRate = %d, want 1", rep.MissingRate)
		}

		if rep.TotalGrossIDR != 1_155_000 || rep.TotalRefundIDR != 360_000 ||
			rep.TotalCommissionIDR != 165_000 || rep.TotalFixedIDR != 2_000_000 ||
			rep.TotalIDR != 2_165_000 {
			t.Errorf("totals = gross %d refund %d commission %d fixed %d total %d, want 1155000/360000/165000/2000000/2165000",
				rep.TotalGrossIDR, rep.TotalRefundIDR, rep.TotalCommissionIDR,
				rep.TotalFixedIDR, rep.TotalIDR)
		}
	})

	t.Run("week of Monday 23 November", func(t *testing.T) {
		rep := payrollReport(t, svc, service.PayrollKindWeek, wibAt(2026, time.November, 25, 10, 0))
		if got := rep.Period.Start.Format("2006-01-02"); got != "2026-11-23" {
			t.Fatalf("period start = %s, want 2026-11-23", got)
		}
		if got := rep.Period.End.Format("2006-01-02"); got != "2026-11-29" {
			t.Fatalf("period end = %s, want 2026-11-29", got)
		}

		// Ayu's two in-week visits; the last-minute-of-November visit and the
		// December 00:05 visit are out. Monthly fixed is omitted on the week view.
		ayuRow := payrollRowNamed(t, rep, "Ayu")
		if ayuRow.Sessions != 2 || ayuRow.GrossIDR != 325_000 || ayuRow.RefundIDR != 150_000 {
			t.Errorf("Ayu sessions/gross/refund = %d/%d/%d, want 2/325000/150000",
				ayuRow.Sessions, ayuRow.GrossIDR, ayuRow.RefundIDR)
		}
		if ayuRow.CommissionIDR != 44_000 || ayuRow.FixedIDR != 0 || ayuRow.TotalIDR != 44_000 {
			t.Errorf("Ayu commission/fixed/total = %d/%d/%d, want 44000/0/44000",
				ayuRow.CommissionIDR, ayuRow.FixedIDR, ayuRow.TotalIDR)
		}

		// Bima: only the Tuesday visit. The Monday 00:05 visit opens the NEXT
		// week, and the weekly fixed is paid once here.
		bimaRow := payrollRowNamed(t, rep, "Bima")
		if bimaRow.Sessions != 1 || bimaRow.GrossIDR != 150_000 {
			t.Errorf("Bima sessions/gross = %d/%d, want 1/150000 (the 30th belongs to the next week)",
				bimaRow.Sessions, bimaRow.GrossIDR)
		}
		if bimaRow.CommissionIDR != 18_000 || bimaRow.FixedIDR != 300_000 || bimaRow.TotalIDR != 318_000 {
			t.Errorf("Bima commission/fixed/total = %d/%d/%d, want 18000/300000/318000",
				bimaRow.CommissionIDR, bimaRow.FixedIDR, bimaRow.TotalIDR)
		}

		// Citra: the Sunday 23:30 visit stays in this week, not the next.
		citraRow := payrollRowNamed(t, rep, "Citra")
		if citraRow.Sessions != 2 || citraRow.GrossIDR != 180_000 || citraRow.CommissionIDR != 18_000 {
			t.Errorf("Citra sessions/gross/commission = %d/%d/%d, want 2/180000/18000",
				citraRow.Sessions, citraRow.GrossIDR, citraRow.CommissionIDR)
		}

		// Dewi's over-cap refund is recorded this week: deducted at the
		// net-default cap with no rate to commission.
		dewiRow := payrollRowNamed(t, rep, "Dewi")
		if dewiRow.GrossIDR != 150_000 || dewiRow.RefundIDR != 150_000 || !dewiRow.RateMissing {
			t.Errorf("Dewi gross/refund/missing = %d/%d/%v, want 150000/150000/true",
				dewiRow.GrossIDR, dewiRow.RefundIDR, dewiRow.RateMissing)
		}
	})

	t.Run("week of Monday 30 November", func(t *testing.T) {
		rep := payrollReport(t, svc, service.PayrollKindWeek, wibAt(2026, time.November, 30, 12, 0))
		if got := rep.Period.Start.Format("2006-01-02"); got != "2026-11-30" {
			t.Fatalf("period start = %s, want 2026-11-30", got)
		}
		ayuRow := payrollRowNamed(t, rep, "Ayu")
		if ayuRow.Sessions != 2 || ayuRow.GrossIDR != 700_000 || ayuRow.CommissionIDR != 175_000 {
			t.Errorf("Ayu sessions/gross/commission = %d/%d/%d, want 2/700000/175000",
				ayuRow.Sessions, ayuRow.GrossIDR, ayuRow.CommissionIDR)
		}
		// Bima: the Monday 00:05 visit (still Monday 30 in WIB) plus the December
		// refund on the November booking.
		bimaRow := payrollRowNamed(t, rep, "Bima")
		if bimaRow.Sessions != 1 || bimaRow.GrossIDR != 50_000 ||
			bimaRow.RefundIDR != 120_000 || bimaRow.NetIDR != -70_000 {
			t.Errorf("Bima sessions/gross/refund/net = %d/%d/%d/%d, want 1/50000/120000/-70000",
				bimaRow.Sessions, bimaRow.GrossIDR, bimaRow.RefundIDR, bimaRow.NetIDR)
		}
		if bimaRow.CommissionIDR != -14_000 || bimaRow.FixedIDR != 300_000 ||
			bimaRow.TotalIDR != 286_000 {
			t.Errorf("Bima commission/fixed/total = %d/%d/%d, want -14000/300000/286000",
				bimaRow.CommissionIDR, bimaRow.FixedIDR, bimaRow.TotalIDR)
		}
		if _, ok := rowByName(rep, "Citra"); ok {
			t.Error("Citra's Sunday 23:30 visit leaked into the next week")
		}
	})

	t.Run("December month", func(t *testing.T) {
		rep := payrollReport(t, svc, service.PayrollKindMonth, wibAt(2026, time.December, 15, 0, 0))
		ayuRow := payrollRowNamed(t, rep, "Ayu")
		if ayuRow.Sessions != 1 || ayuRow.GrossIDR != 400_000 ||
			ayuRow.CommissionIDR != 100_000 || ayuRow.FixedIDR != 500_000 {
			t.Errorf("Ayu sessions/gross/commission/fixed = %d/%d/%d/%d, want 1/400000/100000/500000",
				ayuRow.Sessions, ayuRow.GrossIDR, ayuRow.CommissionIDR, ayuRow.FixedIDR)
		}
		// A refund-only December row: no visits, negative net, weekly fixed over
		// December's four Mondays.
		bimaRow := payrollRowNamed(t, rep, "Bima")
		if bimaRow.Sessions != 0 || bimaRow.GrossIDR != 0 ||
			bimaRow.RefundIDR != 120_000 || bimaRow.NetIDR != -120_000 {
			t.Errorf("Bima sessions/gross/refund/net = %d/%d/%d/%d, want 0/0/120000/-120000",
				bimaRow.Sessions, bimaRow.GrossIDR, bimaRow.RefundIDR, bimaRow.NetIDR)
		}
		if bimaRow.CommissionIDR != -24_000 || bimaRow.FixedIDR != 1_200_000 ||
			bimaRow.TotalIDR != 1_176_000 {
			t.Errorf("Bima commission/fixed/total = %d/%d/%d, want -24000/1200000/1176000",
				bimaRow.CommissionIDR, bimaRow.FixedIDR, bimaRow.TotalIDR)
		}
		// The cancelled December booking's 250,000 refund is a logistics
		// reversal, not commission: only the completed booking's late refund is
		// listed here (R17). Before the status filter this row would show
		// 370,000 deducted and two lines.
		if len(bimaRow.RefundLines) != 1 || bimaRow.RefundLines[0].BookingID != b1 {
			t.Errorf("Bima December refund lines = %+v, want only the completed booking %v",
				bimaRow.RefundLines, b1)
		}
		if _, ok := rowByName(rep, "Citra"); ok {
			t.Error("Citra has no December money but got a row")
		}
	})

	t.Run("October stays separate", func(t *testing.T) {
		rep := payrollReport(t, svc, service.PayrollKindMonth, wibAt(2026, time.October, 15, 0, 0))
		if len(rep.Rows) != 1 {
			t.Fatalf("October rows = %d, want only Bima", len(rep.Rows))
		}
		row := payrollRowNamed(t, rep, "Bima")
		if row.Sessions != 1 || row.GrossIDR != 200_000 || row.RefundIDR != 0 {
			t.Errorf("Bima October = sessions %d gross %d refund %d, want 1/200000/0",
				row.Sessions, row.GrossIDR, row.RefundIDR)
		}
	})

	t.Run("a later refund does not rewrite an earlier period", func(t *testing.T) {
		before := payrollReport(t, svc, service.PayrollKindMonth, wibAt(2026, time.November, 15, 0, 0))
		_ = payrollReport(t, svc, service.PayrollKindMonth, wibAt(2026, time.December, 15, 0, 0))
		after := payrollReport(t, svc, service.PayrollKindMonth, wibAt(2026, time.November, 15, 0, 0))
		if !reflect.DeepEqual(before, after) {
			t.Errorf("November changed after reading December:\nbefore: %+v\nafter:  %+v", before, after)
		}
	})
}

// rowByName reports whether the report has a row for name; the nil-uuid-safe
// helper the absent-staff assertions use.
func rowByName(rep service.PayrollReport, name string) (service.PayrollRow, bool) {
	for _, row := range rep.Rows {
		if row.StaffName == name {
			return row, true
		}
	}
	return service.PayrollRow{}, false
}

// Payroll follows the therapist, not the branch: one period's report spans
// every location. A booking made at another branch still pays its own
// therapist, and one therapist's visits at two branches are a single row — the
// same location-agnostic rule the rate form's ListStaff states on the read side.
func TestPayrollReportSpansLocations(t *testing.T) {
	pool := OpenTestDB(t)
	TruncateAll(t, pool)
	ctx := context.Background()

	branchA := insertLocation(t, pool, "Payroll Branch A")
	branchB := insertLocation(t, pool, "Payroll Branch B")
	treatmentA := insertService(t, pool, branchA, "Treatment A", 100_000)
	treatmentB := insertService(t, pool, branchB, "Treatment B", 200_000)

	ayu := insertStaff(t, pool, branchA, "Ayu")
	bima := insertStaff(t, pool, branchB, "Bima")
	comps := repo.NewStaffCompRepo(pool)
	for _, staffID := range []uuid.UUID{ayu, bima} {
		if err := comps.Create(ctx, &domain.StaffComp{
			StaffID:        staffID,
			PercentBp:      1000,
			Cadence:        domain.CompCadenceMonthly,
			CommissionBase: domain.CompBaseNet,
			EffectiveFrom:  payrollDate(2026, time.January, 1),
		}); err != nil {
			t.Fatalf("create rate for staff %v: %v", staffID, err)
		}
	}

	at := func(day, hour int) time.Time {
		return time.Date(2026, time.November, day, hour, 0, 0, 0, wib)
	}
	insertBooking(t, pool, bookingSeed{
		LocationID: branchA, ServiceID: treatmentA, StaffID: ayu,
		StartAt: at(10, 10), Status: string(domain.BookingCompleted), PriceIDR: 100_000,
	})
	insertBooking(t, pool, bookingSeed{
		LocationID: branchB, ServiceID: treatmentB, StaffID: ayu,
		StartAt: at(11, 10), Status: string(domain.BookingCompleted), PriceIDR: 200_000,
	})
	insertBooking(t, pool, bookingSeed{
		LocationID: branchB, ServiceID: treatmentB, StaffID: bima,
		StartAt: at(12, 10), Status: string(domain.BookingCompleted), PriceIDR: 300_000,
	})

	rep := payrollReport(t, service.NewPayrollService(repo.NewPayrollRepo(pool), comps),
		service.PayrollKindMonth, at(15, 0))
	if len(rep.Rows) != 2 {
		t.Fatalf("rows = %d, want Ayu and Bima from both branches", len(rep.Rows))
	}
	ayuRow := payrollRowNamed(t, rep, "Ayu")
	if ayuRow.Sessions != 2 || ayuRow.GrossIDR != 300_000 || ayuRow.CommissionIDR != 30_000 {
		t.Errorf("Ayu = sessions %d gross %d commission %d, want 2/300000/30000 (both branches)",
			ayuRow.Sessions, ayuRow.GrossIDR, ayuRow.CommissionIDR)
	}
	bimaRow := payrollRowNamed(t, rep, "Bima")
	if bimaRow.Sessions != 1 || bimaRow.GrossIDR != 300_000 || bimaRow.CommissionIDR != 30_000 {
		t.Errorf("Bima = sessions %d gross %d commission %d, want 1/300000/30000",
			bimaRow.Sessions, bimaRow.GrossIDR, bimaRow.CommissionIDR)
	}
}
