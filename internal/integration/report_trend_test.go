//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"spa-booking/internal/service"
)

// insertRefundAt seeds a refund row with an explicit created_at. ReportService
// stamps refunds at now(), so the only way to pin the recorded month/day the
// MTD cutoff reads is to write the column directly — the same row shape the
// service produces.
func (fx *moneyFixture) insertRefundAt(t *testing.T, bookingID uuid.UUID, amount int64, at time.Time, reason string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := fx.pool.Exec(ctx,
		`INSERT INTO refunds (id, booking_id, amount_idr, reason, created_by_id, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		uuid.New(), bookingID, amount, reason, fx.actor, at,
	); err != nil {
		t.Fatalf("insert refund at %s: %v", at.Format(time.RFC3339), err)
	}
}

// TestMonthlyReportThroughDayCutoff executes the month-to-date reading the
// delta line compares: payments and refunds are cut at day N of the WIB month,
// inclusive. The seeded boundary rows are the point — a payment at 23:30 WIB on
// the 15th is in, and a payment at 00:30 WIB on the 16th (17:30 UTC on the 15th)
// is out. Only the AT TIME ZONE conversion and a `day(...) <= N` predicate make
// those two rows differ, so a UTC bucket or an off-by-one day fails here.
func TestMonthlyReportThroughDayCutoff(t *testing.T) {
	fx := newMoneyFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	d14 := time.Date(2026, 9, 14, 10, 0, 0, 0, wib)
	d15 := time.Date(2026, 9, 15, 23, 30, 0, 0, wib)
	d16 := time.Date(2026, 9, 16, 0, 30, 0, 0, wib)
	if got := d16.UTC().Format("2006-01-02"); got != "2026-09-15" {
		t.Fatalf("fixture premise broken: 16th 00:30 WIB is UTC date %s, want 2026-09-15", got)
	}

	b14, _ := fx.paidBooking(t, 100_000, d14, "")
	_, _ = fx.paidBooking(t, 200_000, d15, "")
	b16, _ := fx.paidBooking(t, 400_000, d16, "")

	// One refund before the cutoff and one after it, on the same ledger the
	// monthly report deducts from.
	fx.insertRefundAt(t, b14, 50_000, time.Date(2026, 9, 14, 12, 0, 0, 0, wib), "early refund")
	fx.insertRefundAt(t, b16, 25_000, time.Date(2026, 9, 16, 12, 0, 0, 0, wib), "late refund")

	cases := []struct {
		day                int
		gross, rf, net     int64
		count, refundCount int
	}{
		{13, 0, 0, 0, 0, 0},
		{14, 100_000, 50_000, 50_000, 1, 1},
		{15, 300_000, 50_000, 250_000, 2, 1},
		{16, 700_000, 75_000, 625_000, 3, 2},
	}
	for _, tc := range cases {
		sum, err := fx.svc.MonthlyReportThrough(ctx, "2026-09", fx.loc, tc.day)
		if err != nil {
			t.Fatalf("monthly report through day %d: %v", tc.day, err)
		}
		assertMonth(t, sum, "2026-09", tc.gross, tc.rf, tc.net, tc.count, tc.refundCount)
		if sum.Month != "2026-09" {
			t.Errorf("day %d: month = %q, want 2026-09", tc.day, sum.Month)
		}
	}

	// The whole month is what the page already shows; the cutoff never leaks
	// into the uncut read.
	full, _, _ := fx.month(t, "2026-09")
	assertMonth(t, full, "2026-09", 700_000, 75_000, 625_000, 3, 2)
}

// TestMonthlyNetSeries executes the six-month sparkline read against real rows:
// six chronological months ending at the requested one, each the same cash-basis
// net MonthlyReport defines, with empty months as explicit zeros.
func TestMonthlyNetSeries(t *testing.T) {
	fx := newMoneyFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	b, _ := fx.paidBooking(t, 700_000, time.Date(2026, 9, 12, 10, 0, 0, 0, wib), "")
	fx.insertRefundAt(t, b, 75_000, time.Date(2026, 9, 13, 10, 0, 0, 0, wib), "series refund")

	series, err := fx.svc.MonthlyNetSeries(ctx, "2026-09", fx.loc, 6)
	if err != nil {
		t.Fatalf("monthly net series: %v", err)
	}
	wantMonths := []string{"2026-04", "2026-05", "2026-06", "2026-07", "2026-08", "2026-09"}
	wantNets := []int64{0, 0, 0, 0, 0, 625_000}
	if len(series) != len(wantMonths) {
		t.Fatalf("series length = %d, want %d: %+v", len(series), len(wantMonths), series)
	}
	for i := range wantMonths {
		if series[i].Month != wantMonths[i] || series[i].NetIDR != wantNets[i] {
			t.Errorf("series[%d] = %+v, want %s / %d", i, series[i], wantMonths[i], wantNets[i])
		}
	}

	// A zero-length request still yields the six-month default.
	def, err := fx.svc.MonthlyNetSeries(ctx, "2026-09", fx.loc, 0)
	if err != nil || len(def) != 6 {
		t.Fatalf("default series = (%d, %v), want 6", len(def), err)
	}
}

// TestMonthDeltaFromRealMonths ties the pure delta to the real MTD reads: the
// current month's whole summary (all cash received so far) against the previous
// month cut at the same day-of-month.
func TestMonthDeltaFromRealMonths(t *testing.T) {
	fx := newMoneyFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// August: 100k on the 5th, 900k on the 20th. September: 200k on the 5th,
	// 300k on the 20th. Through day 5 the compare is 200k vs 100k = +100%.
	_, _ = fx.paidBooking(t, 100_000, time.Date(2026, 8, 5, 10, 0, 0, 0, wib), "")
	_, _ = fx.paidBooking(t, 900_000, time.Date(2026, 8, 20, 10, 0, 0, 0, wib), "")
	_, _ = fx.paidBooking(t, 200_000, time.Date(2026, 9, 5, 10, 0, 0, 0, wib), "")
	_, _ = fx.paidBooking(t, 300_000, time.Date(2026, 9, 20, 10, 0, 0, 0, wib), "")

	sep, err := fx.svc.MonthlyReportThrough(ctx, "2026-09", fx.loc, 5)
	if err != nil {
		t.Fatalf("september through 5: %v", err)
	}
	aug, err := fx.svc.MonthlyReportThrough(ctx, "2026-08", fx.loc, 5)
	if err != nil {
		t.Fatalf("august through 5: %v", err)
	}
	bp, asOf, ok := service.MonthDelta(sep, aug, 5)
	if !ok || bp != 10_000 || asOf != "2026-09-05" {
		t.Errorf("delta = (%d, %q, %v), want (10000, 2026-09-05, true)", bp, asOf, ok)
	}

	// The later payments must not leak into the through-5 comparison.
	fullSep, _, _ := fx.month(t, "2026-09")
	if fullSep.NetIDR != 500_000 {
		t.Errorf("september full net = %d, want 500000", fullSep.NetIDR)
	}
	if sep.NetIDR != 200_000 {
		t.Errorf("september through 5 net = %d, want 200000", sep.NetIDR)
	}
}
