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

// The utilization columns mix three sources a unit test cannot join: the weekly
// schedule, the time-off ranges and completed visits. These groups seed all
// three plus every row that must NOT count (a confirmed visit, a visit in the
// next month, another branch) and assert the whole StaffRow.

// insertStaffHours writes one weekly staff_hours window. weekday is the
// Postgres EXTRACT(DOW) convention: 0 = Sunday .. 6 = Saturday.
func insertStaffHours(t *testing.T, pool *pgxpool.Pool, staffID uuid.UUID, weekday int, start, end string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx,
		`INSERT INTO staff_hours (id, staff_id, weekday, start_time, end_time) VALUES ($1,$2,$3,$4::time,$5::time)`,
		uuid.New(), staffID, weekday, start, end); err != nil {
		t.Fatalf("insert staff hours: %v", err)
	}
}

// insertStaffTimeOff writes one leave/blockout range.
func insertStaffTimeOff(t *testing.T, pool *pgxpool.Pool, staffID uuid.UUID, start, end time.Time) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx,
		`INSERT INTO staff_time_off (id, staff_id, start_at, end_at, reason) VALUES ($1,$2,$3,$4,$5)`,
		uuid.New(), staffID, start, end, "utilization fixture"); err != nil {
		t.Fatalf("insert staff time off: %v", err)
	}
}

// TestStaffUtilizationReport is the tagged execution of the staff tab's new
// columns over real rows: November 2026 (five Mondays, so a naive four-week
// figure fails), one two-hour time-off block, two completed visits, plus rows
// that must be excluded by status, month and branch.
func TestStaffUtilizationReport(t *testing.T) {
	fx := newMoneyFixture(t)

	// Staff A carries payments, so the revenue row already exists before the
	// utilization merge. Its schedule: Mondays 09:00–17:00 (November 2026 has
	// five: the 2nd, 9th, 16th, 23rd and 30th) minus the 9th 10:00–12:00.
	insertStaffHours(t, fx.pool, fx.staff, 1, "09:00", "17:00")
	insertStaffTimeOff(t, fx.pool, fx.staff,
		time.Date(2026, 11, 9, 10, 0, 0, 0, wib), time.Date(2026, 11, 9, 12, 0, 0, 0, wib))
	// A block outside working hours must not reduce the schedule.
	insertStaffTimeOff(t, fx.pool, fx.staff,
		time.Date(2026, 11, 8, 18, 0, 0, 0, wib), time.Date(2026, 11, 8, 20, 0, 0, 0, wib))

	// A's two completed visits: 90 + 60 = 150 booked minutes, Rp350.000 of
	// service price. The 90-minute booking is deliberately longer than the
	// seeded 60-minute service: booked minutes follow the booking's own
	// start/end (the real calendar), not the catalog duration.
	first := time.Date(2026, 11, 2, 9, 0, 0, 0, wib)
	firstID := insertBooking(t, fx.pool, bookingSeed{
		CustomerID: &fx.customer, LocationID: fx.loc, ServiceID: fx.service, StaffID: fx.staff,
		StartAt: first, EndAt: first.Add(90 * time.Minute),
		PriceIDR: 200_000, Status: string(domain.BookingCompleted),
	})
	second := time.Date(2026, 11, 16, 13, 0, 0, 0, wib)
	insertBooking(t, fx.pool, bookingSeed{
		CustomerID: &fx.customer, LocationID: fx.loc, ServiceID: fx.service, StaffID: fx.staff,
		StartAt: second, EndAt: second.Add(time.Hour),
		PriceIDR: 150_000, Status: string(domain.BookingCompleted),
	})
	// Only completed visits count: a confirmed two-hour slot adds nothing.
	confirmed := time.Date(2026, 11, 4, 10, 0, 0, 0, wib)
	insertBooking(t, fx.pool, bookingSeed{
		CustomerID: &fx.customer, LocationID: fx.loc, ServiceID: fx.service, StaffID: fx.staff,
		StartAt: confirmed, EndAt: confirmed.Add(2 * time.Hour),
		PriceIDR: 200_000, Status: string(domain.BookingConfirmed),
	})
	// A completed visit in December must stay in December, whatever its price.
	nextMonth := time.Date(2026, 12, 7, 9, 0, 0, 0, wib)
	insertBooking(t, fx.pool, bookingSeed{
		CustomerID: &fx.customer, LocationID: fx.loc, ServiceID: fx.service, StaffID: fx.staff,
		StartAt: nextMonth, EndAt: nextMonth.Add(time.Hour),
		PriceIDR: 200_000, Status: string(domain.BookingCompleted),
	})
	// The payment keeps the payment-based Gross at Rp200.000 — deliberately
	// different from the Rp350.000 completed-visit numerator of Rp/hour.
	paidAt := time.Date(2026, 11, 5, 10, 0, 0, 0, wib)
	insertPayment(t, fx.pool, paymentSeed{BookingID: firstID, AmountIDR: 200_000, PaidAt: &paidAt})

	// Staff B: one completed visit, no schedule at all — the "—" path.
	staffB := insertStaff(t, fx.pool, fx.loc, "Bidan Tanpa Jadwal")
	bStart := time.Date(2026, 11, 10, 9, 0, 0, 0, wib)
	insertBooking(t, fx.pool, bookingSeed{
		CustomerID: &fx.customer, LocationID: fx.loc, ServiceID: fx.service, StaffID: staffB,
		StartAt: bStart, EndAt: bStart.Add(time.Hour),
		PriceIDR: 150_000, Status: string(domain.BookingCompleted),
	})

	// Staff C: a schedule but no visits — utilization 0%, no revenue row.
	staffC := insertStaff(t, fx.pool, fx.loc, "Bidan Terjadwal")
	insertStaffHours(t, fx.pool, staffC, 1, "09:00", "17:00")

	// Another branch's therapist with both a schedule and a completed visit:
	// the location filter must exclude it from this month's staff table.
	otherLoc := insertLocation(t, fx.pool, "Other Branch")
	otherSvc := insertService(t, fx.pool, otherLoc, "Other Treatment", 90_000)
	staffD := insertStaff(t, fx.pool, otherLoc, "Other Therapist")
	insertStaffHours(t, fx.pool, staffD, 1, "09:00", "17:00")
	dStart := time.Date(2026, 11, 10, 13, 0, 0, 0, wib)
	insertBooking(t, fx.pool, bookingSeed{
		CustomerID: &fx.customer, LocationID: otherLoc, ServiceID: otherSvc, StaffID: staffD,
		StartAt: dStart, EndAt: dStart.Add(time.Hour),
		PriceIDR: 90_000, Status: string(domain.BookingCompleted),
	})

	sum, _, _ := fx.monthWithUtil(t, "2026-11")
	if len(sum.StaffRows) != 3 {
		t.Fatalf("staff rows = %d, want 3 (A paid, B visits only, C schedule only): %+v", len(sum.StaffRows), sum.StaffRows)
	}
	byID := map[uuid.UUID]domain.StaffRow{}
	for _, row := range sum.StaffRows {
		byID[row.StaffID] = row
	}
	if _, ok := byID[staffD]; ok {
		t.Errorf("other-branch therapist leaked into the branch report")
	}

	a := byID[fx.staff]
	// 5 Mondays × 480 min − 120 min time-off (the Sunday block subtracts 0).
	if a.ScheduledMin != 2280 {
		t.Errorf("staff A scheduled = %d minutes, want 2280", a.ScheduledMin)
	}
	if a.BookedMin != 150 {
		t.Errorf("staff A booked = %d minutes, want 150 (90 + 60, completed only)", a.BookedMin)
	}
	if a.RpPerHour != 9_211 {
		t.Errorf("staff A Rp/hour = %d, want 9211 (Rp350.000 / 38 hours, rounded)", a.RpPerHour)
	}
	if a.OccupancyBp != 658 {
		t.Errorf("staff A occupancy = %d bp, want 658 (150 / 2280)", a.OccupancyBp)
	}
	// The payment-based revenue columns are untouched by the merge.
	if a.GrossIDR != 200_000 || a.NetIDR != 200_000 || a.Sessions != 1 {
		t.Errorf("staff A revenue = %+v, want the payment-based 200000/200000/1", a)
	}

	b := byID[staffB]
	if b.ScheduledMin != 0 || b.BookedMin != 60 || b.RpPerHour != 0 || b.OccupancyBp != 0 {
		t.Errorf("staff B = %+v, want 60 booked with no schedule and zero ratios", b)
	}
	c := byID[staffC]
	if c.ScheduledMin != 2400 || c.BookedMin != 0 || c.RpPerHour != 0 || c.OccupancyBp != 0 {
		t.Errorf("staff C = %+v, want 2400 scheduled and zero booked", c)
	}

	// The plain monthly path (every other tab, the CSV, the sparkline) must not
	// pay for the utilization queries or change its rows.
	plain, _, _ := fx.month(t, "2026-11")
	for _, row := range plain.StaffRows {
		if row.ScheduledMin != 0 || row.BookedMin != 0 || row.RpPerHour != 0 || row.OccupancyBp != 0 {
			t.Errorf("plain MonthlyReport carried utilization for %s: %+v", row.Name, row)
		}
	}
}

// monthWithUtil runs the staff-tab read: MonthlyReport plus the utilization
// merge for the same month and branch.
func (fx *moneyFixture) monthWithUtil(t *testing.T, month string) (domain.ReportSummary, []domain.ReportLine, []domain.ReportRefund) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sum, lines, refunds, err := fx.svc.MonthlyReportWithUtilization(ctx, month, fx.loc)
	if err != nil {
		t.Fatalf("monthly report with utilization %s: %v", month, err)
	}
	return sum, lines, refunds
}
