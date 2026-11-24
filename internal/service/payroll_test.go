package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"

	"github.com/google/uuid"
)

// The payroll computation is pure integer money math over two repository reads
// (completed visits and recorded refunds). The helpers below pin the arithmetic
// directly; the fakes drive the whole report so the refund cap, the fixed
// cadence matrix and the missing-rate flag are exercised without a database.
// The real SQL window (a WIB Sunday 23:30 visit, a month-edge 23:30 visit) is
// the tagged integration group's job.

// fakePayrollVisits answers the two period reads. It records the window it was
// asked for, because a WIB boundary bug lives in the range the service passes,
// not in the rows it gets back.
type fakePayrollVisits struct {
	visits  []repo.PayrollVisitRow
	refunds []repo.PayrollRefundRow
	from    time.Time
	to      time.Time
	calls   int
}

func (f *fakePayrollVisits) CompletedVisits(_ context.Context, from, to time.Time) ([]repo.PayrollVisitRow, error) {
	f.calls++
	f.from, f.to = from, to
	return f.visits, nil
}

func (f *fakePayrollVisits) RefundsInRange(_ context.Context, from, to time.Time) ([]repo.PayrollRefundRow, error) {
	f.calls++
	f.from, f.to = from, to
	return f.refunds, nil
}

// fakePayrollRates resolves a rate per staff member; absence is (nil, nil), the
// same contract StaffCompRepo.EffectiveFor ships.
type fakePayrollRates struct {
	byStaff map[uuid.UUID]*domain.StaffComp
	err     error
}

func (f *fakePayrollRates) EffectiveFor(_ context.Context, staffID uuid.UUID, _ time.Time) (*domain.StaffComp, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byStaff[staffID], nil
}

func payrollService(visits *fakePayrollVisits, rates *fakePayrollRates) *PayrollService {
	if rates == nil {
		rates = &fakePayrollRates{byStaff: map[uuid.UUID]*domain.StaffComp{}}
	}
	return &PayrollService{visits: visits, rates: rates}
}

func payrollVisit(staffID uuid.UUID, name string, at time.Time, price, discount int64) repo.PayrollVisitRow {
	return repo.PayrollVisitRow{
		BookingID:   uuid.New(),
		StaffID:     staffID,
		StaffName:   name,
		StartAt:     at,
		ServiceName: "Treatment",
		PriceIDR:    price,
		DiscountIDR: discount,
	}
}

func payrollRefund(staffID uuid.UUID, name string, at time.Time, amount, price, discount int64) repo.PayrollRefundRow {
	return repo.PayrollRefundRow{
		RefundID:    uuid.New(),
		BookingID:   uuid.New(),
		StaffID:     staffID,
		StaffName:   name,
		CreatedAt:   at,
		AmountIDR:   amount,
		Reason:      "refund",
		PriceIDR:    price,
		DiscountIDR: discount,
	}
}

func payrollRow(t *testing.T, rep PayrollReport, name string) PayrollRow {
	t.Helper()
	for _, row := range rep.Rows {
		if row.StaffName == name {
			return row
		}
	}
	t.Fatalf("report has no row for %q; rows: %s", name, payrollNames(rep))
	return PayrollRow{}
}

func payrollNames(rep PayrollReport) string {
	out := ""
	for i, row := range rep.Rows {
		if i > 0 {
			out += ", "
		}
		out += row.StaffName
	}
	return out
}

// TestPayrollPeriodFor pins the period normalisation: any instant inside a WIB
// week folds to that week's Monday, any instant inside a month to its first
// day. The instants are built in UTC where the note says so, because that is
// exactly where a naive Month()/Weekday() read disagrees with WIB.
func TestPayrollPeriodFor(t *testing.T) {
	cases := []struct {
		name      string
		kind      string
		start     time.Time
		wantKind  string
		wantStart string
		wantEnd   string
	}{
		{
			name:      "week from a Wednesday",
			kind:      PayrollKindWeek,
			start:     time.Date(2026, 11, 25, 10, 0, 0, 0, wib),
			wantKind:  PayrollKindWeek,
			wantStart: "2026-11-23",
			wantEnd:   "2026-11-29",
		},
		{
			name:      "week from Sunday 23:30 WIB stays in its own week",
			kind:      PayrollKindWeek,
			start:     time.Date(2026, 11, 29, 23, 30, 0, 0, wib),
			wantKind:  PayrollKindWeek,
			wantStart: "2026-11-23",
			wantEnd:   "2026-11-29",
		},
		{
			// Monday 00:30 WIB is still Sunday 17:30 UTC. A UTC-normalised
			// normaliser would fold this into the previous week.
			name:      "week from Monday 00:30 WIB read as a UTC instant",
			kind:      PayrollKindWeek,
			start:     time.Date(2026, 11, 22, 17, 30, 0, 0, time.UTC),
			wantKind:  PayrollKindWeek,
			wantStart: "2026-11-23",
			wantEnd:   "2026-11-29",
		},
		{
			name:      "month from mid-November",
			kind:      PayrollKindMonth,
			start:     time.Date(2026, 11, 24, 10, 0, 0, 0, wib),
			wantKind:  PayrollKindMonth,
			wantStart: "2026-11-01",
			wantEnd:   "2026-11-30",
		},
		{
			name:      "month from the last day at 23:30 WIB",
			kind:      PayrollKindMonth,
			start:     time.Date(2026, 11, 30, 23, 30, 0, 0, wib),
			wantKind:  PayrollKindMonth,
			wantStart: "2026-11-01",
			wantEnd:   "2026-11-30",
		},
		{
			name:      "month from December 1st 00:05 WIB read as a UTC instant",
			kind:      PayrollKindMonth,
			start:     time.Date(2026, 11, 30, 17, 5, 0, 0, time.UTC),
			wantKind:  PayrollKindMonth,
			wantStart: "2026-12-01",
			wantEnd:   "2026-12-31",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PayrollPeriodFor(tc.kind, tc.start)
			if err != nil {
				t.Fatalf("PayrollPeriodFor(%q, %v): %v", tc.kind, tc.start, err)
			}
			if got.Kind != tc.wantKind {
				t.Errorf("kind = %q, want %q", got.Kind, tc.wantKind)
			}
			if s := got.Start.In(wib).Format("2006-01-02"); s != tc.wantStart {
				t.Errorf("start = %s, want %s", s, tc.wantStart)
			}
			if e := got.End.In(wib).Format("2006-01-02"); e != tc.wantEnd {
				t.Errorf("end = %s, want %s", e, tc.wantEnd)
			}
			if got.End.Before(got.Start) {
				t.Errorf("end %v is before start %v", got.End, got.Start)
			}
		})
	}

	for _, kind := range []string{"daily", "", "weekly", "monthly"} {
		if _, err := PayrollPeriodFor(kind, time.Now()); err == nil {
			t.Errorf("PayrollPeriodFor(%q) accepted an unknown period kind", kind)
		}
	}
}

// TestBaseFor pins the two bases and the per-visit floor. A discount larger
// than the price (a data-entry mistake the DB allows) must never make the visit
// negative: the worst a visit can do is contribute nothing.
func TestBaseFor(t *testing.T) {
	cases := []struct {
		name            string
		price, discount int64
		base            string
		want            int64
	}{
		{"list ignores the discount", 150_000, 25_000, domain.CompBaseList, 150_000},
		{"net subtracts the discount", 150_000, 25_000, domain.CompBaseNet, 125_000},
		{"net with no discount", 150_000, 0, domain.CompBaseNet, 150_000},
		{"net floors at zero", 150_000, 200_000, domain.CompBaseNet, 0},
		{"list with an over-discount is untouched", 150_000, 200_000, domain.CompBaseList, 150_000},
		{"zero price", 0, 0, domain.CompBaseNet, 0},
		// An unconfigured base falls back to the spec's default (net) rather
		// than inventing list: a missing rate row is a normal state, and its
		// gross must still be shown.
		{"unknown base falls back to net", 100_000, 40_000, "", 60_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := baseFor(tc.price, tc.discount, tc.base); got != tc.want {
				t.Errorf("baseFor(%d, %d, %q) = %d, want %d", tc.price, tc.discount, tc.base, got, tc.want)
			}
		})
	}
}

// TestCommissionBp pins commission = round(net × bp / 10000) to the nearest
// Rp 1,000, half away from zero. Integer-only: the raw product is compared to
// 10000×1000 halves, never divided through a float.
func TestCommissionBp(t *testing.T) {
	cases := []struct {
		name string
		net  int64
		bp   int
		want int64
	}{
		{"whole percent of a round net", 1_000_000, 2500, 250_000},
		{"zero percent", 1_000_000, 0, 0},
		{"raw 100 rounds down to 0", 1_000_000, 1, 0},
		{"raw 499 rounds down", 4_990_000, 1, 0},
		{"raw 500 rounds up", 5_000_000, 1, 1_000},
		{"raw 1500 rounds up", 15_000_000, 1, 2_000},
		{"non-tie rounds to the nearest thousand", 1_234_567, 2500, 309_000},
		{"exact tie at 12500 goes up", 50_000, 2500, 13_000},
		// Negative symmetry: a late refund can make a period's net negative,
		// and half away from zero must mirror the positive side.
		{"negative exact percent", -1_000_000, 2500, -250_000},
		{"negative raw 499 rounds toward zero", -4_990_000, 1, 0},
		{"negative raw 500 rounds away from zero", -5_000_000, 1, -1_000},
		{"negative exact tie goes away from zero", -50_000, 2500, -13_000},
		{"zero net", 0, 2500, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := commissionBp(tc.net, tc.bp); got != tc.want {
				t.Errorf("commissionBp(%d, %d) = %d, want %d", tc.net, tc.bp, got, tc.want)
			}
		})
	}
}

// TestResolveFixed pins R9: a fixed amount is paid once per its own cadence.
// On the month view a weekly fixed is repeated for every contained WIB week; on
// the week view a monthly fixed is omitted (the page says "counted on the
// month"), never prorated and never shown twice.
func TestResolveFixed(t *testing.T) {
	cases := []struct {
		name    string
		fixed   int64
		cadence string
		kind    string
		weeks   int
		want    int64
	}{
		{"weekly fixed on the week view", 300_000, domain.CompCadenceWeekly, PayrollKindWeek, 1, 300_000},
		{"weekly fixed on the month view sums contained weeks", 300_000, domain.CompCadenceWeekly, PayrollKindMonth, 5, 1_500_000},
		{"monthly fixed on the month view", 500_000, domain.CompCadenceMonthly, PayrollKindMonth, 5, 500_000},
		{"monthly fixed on the week view is omitted", 500_000, domain.CompCadenceMonthly, PayrollKindWeek, 1, 0},
		{"zero fixed", 0, domain.CompCadenceWeekly, PayrollKindMonth, 4, 0},
		{"unknown cadence pays nothing", 500_000, "daily", PayrollKindMonth, 4, 0},
		{"unknown kind pays nothing", 500_000, domain.CompCadenceMonthly, "daily", 4, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveFixed(tc.fixed, tc.cadence, tc.kind, tc.weeks); got != tc.want {
				t.Errorf("resolveFixed(%d, %q, %q, %d) = %d, want %d",
					tc.fixed, tc.cadence, tc.kind, tc.weeks, got, tc.want)
			}
		})
	}
}

// TestWeeksInMonth counts WIB Mondays. November 2026 has five (2, 9, 16, 23,
// 30) and December four; June 2026 starts on a Monday and still has five. The
// count is what multiplies a weekly fixed on the month view.
func TestWeeksInMonth(t *testing.T) {
	cases := []struct {
		name  string
		start time.Time
		want  int
	}{
		{"November 2026", time.Date(2026, 11, 1, 0, 0, 0, 0, wib), 5},
		{"December 2026", time.Date(2026, 12, 1, 0, 0, 0, 0, wib), 4},
		{"February 2026", time.Date(2026, 2, 1, 0, 0, 0, 0, wib), 4},
		{"June 2026 starts on a Monday", time.Date(2026, 6, 1, 0, 0, 0, 0, wib), 5},
		{"February 2028 leap year", time.Date(2028, 2, 1, 0, 0, 0, 0, wib), 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := weeksInMonth(tc.start); got != tc.want {
				t.Errorf("weeksInMonth(%s) = %d, want %d", tc.start.Format("2006-01"), got, tc.want)
			}
		})
	}
}

// TestPayrollReportRows executes the whole composition for one WIB month: a net
// therapist whose discount reduces the gross and whose commission rounds up
// (112,500 → 113,000), and a list therapist whose discount does not touch the
// gross and whose weekly fixed repeats across the month's five Mondays.
func TestPayrollReportRows(t *testing.T) {
	ayu := uuid.New()
	bima := uuid.New()
	visits := &fakePayrollVisits{
		visits: []repo.PayrollVisitRow{
			payrollVisit(ayu, "Ayu", time.Date(2026, 11, 23, 10, 0, 0, 0, wib), 200_000, 50_000),
			payrollVisit(ayu, "Ayu", time.Date(2026, 11, 28, 10, 0, 0, 0, wib), 300_000, 0),
			payrollVisit(bima, "Bima", time.Date(2026, 11, 24, 10, 0, 0, 0, wib), 150_000, 20_000),
		},
	}
	rates := &fakePayrollRates{byStaff: map[uuid.UUID]*domain.StaffComp{
		ayu: {StaffID: ayu, PercentBp: 2500, FixedIDR: 500_000,
			Cadence: domain.CompCadenceMonthly, CommissionBase: domain.CompBaseNet},
		bima: {StaffID: bima, PercentBp: 2000, FixedIDR: 300_000,
			Cadence: domain.CompCadenceWeekly, CommissionBase: domain.CompBaseList},
	}}

	rep, err := payrollService(visits, rates).Report(context.Background(), PayrollKindMonth, time.Date(2026, 11, 15, 0, 0, 0, 0, wib))
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if len(rep.Rows) != 2 {
		t.Fatalf("report rows = %d, want 2", len(rep.Rows))
	}
	// Sorted by StaffName, not by the (random) staff id or the query order.
	if rep.Rows[0].StaffName != "Ayu" || rep.Rows[1].StaffName != "Bima" {
		t.Fatalf("rows are not name-sorted: %s", payrollNames(rep))
	}

	a := payrollRow(t, rep, "Ayu")
	if a.Sessions != 2 || a.GrossIDR != 450_000 || a.RefundIDR != 0 || a.NetIDR != 450_000 {
		t.Errorf("Ayu visit totals = %+v, want 2 sessions, gross 450000, net 450000", a)
	}
	if a.PercentBp != 2500 || a.Base != domain.CompBaseNet || a.RateMissing {
		t.Errorf("Ayu rate = bp %d base %q missing %v, want 2500/net/false", a.PercentBp, a.Base, a.RateMissing)
	}
	if a.CommissionIDR != 113_000 || a.FixedIDR != 500_000 || a.TotalIDR != 613_000 {
		t.Errorf("Ayu commission/fixed/total = %d/%d/%d, want 113000/500000/613000",
			a.CommissionIDR, a.FixedIDR, a.TotalIDR)
	}
	// The detail list reads the base the row actually counted, so a net row's
	// 200k/50k visit must show 150k, not the frozen list price.
	if len(a.Visits) != 2 || a.Visits[0].BaseIDR != 150_000 || a.Visits[1].BaseIDR != 300_000 {
		t.Errorf("Ayu visit bases = %+v, want 150000 and 300000", a.Visits)
	}

	b := payrollRow(t, rep, "Bima")
	if b.GrossIDR != 150_000 {
		t.Errorf("Bima list gross = %d, want 150000 (the discount is ignored)", b.GrossIDR)
	}
	if b.CommissionIDR != 30_000 || b.FixedIDR != 1_500_000 || b.TotalIDR != 1_530_000 {
		t.Errorf("Bima commission/fixed/total = %d/%d/%d, want 30000/1500000/1530000",
			b.CommissionIDR, b.FixedIDR, b.TotalIDR)
	}
	// A list row ignores the discount in its base too.
	if b.Visits[0].BaseIDR != 150_000 {
		t.Errorf("Bima list visit base = %d, want 150000", b.Visits[0].BaseIDR)
	}

	if rep.TotalGrossIDR != 600_000 || rep.TotalRefundIDR != 0 ||
		rep.TotalCommissionIDR != 143_000 || rep.TotalFixedIDR != 2_000_000 ||
		rep.TotalIDR != 2_143_000 {
		t.Errorf("totals = gross %d refund %d commission %d fixed %d total %d, want 600000/0/143000/2000000/2143000",
			rep.TotalGrossIDR, rep.TotalRefundIDR, rep.TotalCommissionIDR, rep.TotalFixedIDR, rep.TotalIDR)
	}
	if rep.Period.Kind != PayrollKindMonth || rep.Period.Start.Format("2006-01-02") != "2026-11-01" {
		t.Errorf("report period = %+v, want the normalised November month", rep.Period)
	}
}

// TestPayrollRefundCap pins R2's cap: per booking per period, the deductible
// refunds stop at the booking's base under the row's commission_base. The line
// list still carries the recorded amounts (what actually left the till), so a
// capped period's lines can sum to more than RefundIDR.
func TestPayrollRefundCap(t *testing.T) {
	staff := uuid.New()
	// One booking: base is list 100,000. Three refunds total 115,000.
	booking := uuid.New()
	base := time.Date(2026, 11, 10, 9, 0, 0, 0, wib)
	lines := []repo.PayrollRefundRow{
		{RefundID: uuid.New(), BookingID: booking, StaffID: staff, StaffName: "Cici",
			CreatedAt: base, AmountIDR: 60_000, PriceIDR: 100_000, DiscountIDR: 10_000},
		{RefundID: uuid.New(), BookingID: booking, StaffID: staff, StaffName: "Cici",
			CreatedAt: base.Add(time.Hour), AmountIDR: 50_000, PriceIDR: 100_000, DiscountIDR: 10_000},
		{RefundID: uuid.New(), BookingID: booking, StaffID: staff, StaffName: "Cici",
			CreatedAt: base.Add(2 * time.Hour), AmountIDR: 5_000, PriceIDR: 100_000, DiscountIDR: 10_000},
	}
	visit := payrollVisit(staff, "Cici", time.Date(2026, 11, 9, 10, 0, 0, 0, wib), 100_000, 10_000)
	visit.BookingID = booking

	rep, err := payrollService(
		&fakePayrollVisits{visits: []repo.PayrollVisitRow{visit}, refunds: lines},
		&fakePayrollRates{byStaff: map[uuid.UUID]*domain.StaffComp{
			staff: {StaffID: staff, PercentBp: 2000, Cadence: domain.CompCadenceMonthly, CommissionBase: domain.CompBaseList},
		}},
	).Report(context.Background(), PayrollKindMonth, time.Date(2026, 11, 15, 0, 0, 0, 0, wib))
	if err != nil {
		t.Fatalf("Report: %v", err)
	}

	row := payrollRow(t, rep, "Cici")
	if row.GrossIDR != 100_000 || row.RefundIDR != 100_000 || row.NetIDR != 0 {
		t.Errorf("capped row = gross %d refund %d net %d, want 100000/100000/0",
			row.GrossIDR, row.RefundIDR, row.NetIDR)
	}
	if row.CommissionIDR != 0 || row.TotalIDR != 0 {
		t.Errorf("commission/total = %d/%d, want 0/0", row.CommissionIDR, row.TotalIDR)
	}
	if len(row.RefundLines) != 3 {
		t.Fatalf("refund lines = %d, want all 3 recorded refunds", len(row.RefundLines))
	}
	wantAmounts := []int64{60_000, 50_000, 5_000}
	for i, ln := range row.RefundLines {
		if ln.AmountIDR != wantAmounts[i] {
			t.Errorf("refund line %d amount = %d, want the recorded %d", i, ln.AmountIDR, wantAmounts[i])
		}
		if ln.BookingID != booking {
			t.Errorf("refund line %d booking = %v, want %v", i, ln.BookingID, booking)
		}
	}
	if rep.TotalRefundIDR != 100_000 {
		t.Errorf("total refund = %d, want the capped 100000", rep.TotalRefundIDR)
	}
}

// TestPayrollRefundOnlyNegativePeriod pins the late-refund case: a refund whose
// visit was paid in an earlier period lands in its own period as a row with no
// visits, a negative net, and a negative commission rounded away from zero.
func TestPayrollRefundOnlyNegativePeriod(t *testing.T) {
	staff := uuid.New()
	refund := payrollRefund(staff, "Dewi", time.Date(2026, 12, 2, 12, 0, 0, 0, wib), 50_000, 200_000, 50_000)

	rep, err := payrollService(
		&fakePayrollVisits{refunds: []repo.PayrollRefundRow{refund}},
		&fakePayrollRates{byStaff: map[uuid.UUID]*domain.StaffComp{
			staff: {StaffID: staff, PercentBp: 2500, Cadence: domain.CompCadenceMonthly, CommissionBase: domain.CompBaseNet},
		}},
	).Report(context.Background(), PayrollKindMonth, time.Date(2026, 12, 10, 0, 0, 0, 0, wib))
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if len(rep.Rows) != 1 {
		t.Fatalf("rows = %d, want the refund-only row", len(rep.Rows))
	}
	row := rep.Rows[0]
	if row.Sessions != 0 || row.GrossIDR != 0 {
		t.Errorf("refund-only row has work: sessions %d gross %d", row.Sessions, row.GrossIDR)
	}
	if row.RefundIDR != 50_000 || row.NetIDR != -50_000 {
		t.Errorf("refund/net = %d/%d, want 50000/-50000", row.RefundIDR, row.NetIDR)
	}
	// -50,000 × 25% = -12,500 → half away from zero → -13,000.
	if row.CommissionIDR != -13_000 || row.TotalIDR != -13_000 {
		t.Errorf("commission/total = %d/%d, want -13000/-13000", row.CommissionIDR, row.TotalIDR)
	}
	if rep.TotalIDR != -13_000 {
		t.Errorf("report total = %d, want -13000", rep.TotalIDR)
	}
}

// TestPayrollMissingRateRow pins "never silently omitted": a therapist with
// completed visits and no rate row is listed, flagged, and priced at zero
// commission/fixed while its gross still shows. The gross uses the spec's
// default base (net) and the row's Base stays empty so the page can say "rate
// not configured" instead of naming a base that was never chosen.
func TestPayrollMissingRateRow(t *testing.T) {
	missing := uuid.New()
	rated := uuid.New()
	visits := &fakePayrollVisits{visits: []repo.PayrollVisitRow{
		payrollVisit(missing, "Eka", time.Date(2026, 11, 23, 10, 0, 0, 0, wib), 180_000, 30_000),
		payrollVisit(rated, "Fajar", time.Date(2026, 11, 23, 11, 0, 0, 0, wib), 100_000, 0),
	}}
	rates := &fakePayrollRates{byStaff: map[uuid.UUID]*domain.StaffComp{
		rated: {StaffID: rated, PercentBp: 1000, Cadence: domain.CompCadenceMonthly, CommissionBase: domain.CompBaseList},
	}}

	rep, err := payrollService(visits, rates).Report(context.Background(), PayrollKindMonth, time.Date(2026, 11, 23, 0, 0, 0, 0, wib))
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if rep.MissingRate != 1 {
		t.Errorf("MissingRate = %d, want 1", rep.MissingRate)
	}
	row := payrollRow(t, rep, "Eka")
	if !row.RateMissing {
		t.Error("Eka.RateMissing = false, want true")
	}
	if row.Base != "" || row.PercentBp != 0 {
		t.Errorf("missing-rate base/bp = %q/%d, want empty/0", row.Base, row.PercentBp)
	}
	if row.GrossIDR != 150_000 || row.NetIDR != 150_000 {
		t.Errorf("missing-rate gross/net = %d/%d, want 150000/150000 (net fallback)", row.GrossIDR, row.NetIDR)
	}
	if row.CommissionIDR != 0 || row.FixedIDR != 0 || row.TotalIDR != 0 {
		t.Errorf("missing-rate commission/fixed/total = %d/%d/%d, want zeros",
			row.CommissionIDR, row.FixedIDR, row.TotalIDR)
	}
	if other := payrollRow(t, rep, "Fajar"); other.RateMissing {
		t.Error("Fajar.RateMissing = true, want false")
	}
	if rep.TotalIDR != 10_000 {
		t.Errorf("report total = %d, want 10000", rep.TotalIDR)
	}
}

// TestPayrollFixedCadenceMatrix runs the four cadence/view combinations through
// the real report, so the fixed column is proven at the point the page reads it
// rather than only in the helper's own table.
func TestPayrollFixedCadenceMatrix(t *testing.T) {
	ids := map[string]uuid.UUID{"W-Week": uuid.New(), "W-Month": uuid.New(), "M-Week": uuid.New(), "M-Month": uuid.New()}
	visits := &fakePayrollVisits{}
	rates := &fakePayrollRates{byStaff: map[uuid.UUID]*domain.StaffComp{}}
	for name, id := range ids {
		visits.visits = append(visits.visits, payrollVisit(id, name, time.Date(2026, 11, 23, 10, 0, 0, 0, wib), 100_000, 0))
	}
	rates.byStaff[ids["W-Week"]] = &domain.StaffComp{StaffID: ids["W-Week"], FixedIDR: 300_000, Cadence: domain.CompCadenceWeekly}
	rates.byStaff[ids["W-Month"]] = &domain.StaffComp{StaffID: ids["W-Month"], FixedIDR: 300_000, Cadence: domain.CompCadenceWeekly}
	rates.byStaff[ids["M-Week"]] = &domain.StaffComp{StaffID: ids["M-Week"], FixedIDR: 500_000, Cadence: domain.CompCadenceMonthly}
	rates.byStaff[ids["M-Month"]] = &domain.StaffComp{StaffID: ids["M-Month"], FixedIDR: 500_000, Cadence: domain.CompCadenceMonthly}

	svc := payrollService(visits, rates)
	week, err := svc.Report(context.Background(), PayrollKindWeek, time.Date(2026, 11, 23, 0, 0, 0, 0, wib))
	if err != nil {
		t.Fatalf("week Report: %v", err)
	}
	if got := payrollRow(t, week, "W-Week").FixedIDR; got != 300_000 {
		t.Errorf("weekly fixed on the week view = %d, want 300000", got)
	}
	if got := payrollRow(t, week, "M-Week").FixedIDR; got != 0 {
		t.Errorf("monthly fixed on the week view = %d, want 0", got)
	}
	if !payrollRow(t, week, "M-Week").FixedOnMonth {
		t.Errorf("monthly fixed omitted on the week view must carry FixedOnMonth for the page's note")
	}

	month, err := svc.Report(context.Background(), PayrollKindMonth, time.Date(2026, 11, 23, 0, 0, 0, 0, wib))
	if err != nil {
		t.Fatalf("month Report: %v", err)
	}
	if got := payrollRow(t, month, "W-Month").FixedIDR; got != 1_500_000 {
		t.Errorf("weekly fixed on the month view = %d, want 300000 × 5 = 1500000", got)
	}
	if got := payrollRow(t, month, "M-Month").FixedIDR; got != 500_000 {
		t.Errorf("monthly fixed on the month view = %d, want 500000 once", got)
	}
	if payrollRow(t, month, "M-Month").FixedOnMonth {
		t.Errorf("monthly fixed paid on the month view must not carry the week-view note")
	}
}

// TestPayrollReportWeekBoundary pins what the service hands the SQL layer. The
// Sunday 23:30 WIB visit must stay in its own week, and the Monday 00:05 WIB
// visit (Sunday evening in UTC) must open the next one — the report passes the
// half-open WIB window the repo reads, so this is where an off-by-one lives.
func TestPayrollReportWeekBoundary(t *testing.T) {
	visits := &fakePayrollVisits{}
	svc := payrollService(visits, nil)

	sundayLate := time.Date(2026, 11, 29, 23, 30, 0, 0, wib)
	rep, err := svc.Report(context.Background(), PayrollKindWeek, sundayLate)
	if err != nil {
		t.Fatalf("Report(Sunday 23:30): %v", err)
	}
	if got := visits.from.In(wib).Format("2006-01-02 15:04"); got != "2026-11-23 00:00" {
		t.Errorf("week query start = %s, want 2026-11-23 00:00", got)
	}
	if got := visits.to.In(wib).Format("2006-01-02 15:04"); got != "2026-11-29 00:00" {
		t.Errorf("week query end = %s, want 2026-11-29 00:00", got)
	}
	if endExclusive := rep.Period.End.In(wib).AddDate(0, 0, 1); !sundayLate.Before(endExclusive) {
		t.Errorf("Sunday 23:30 WIB is not inside [%v, %v)", rep.Period.Start, endExclusive)
	}

	mondayEarly := time.Date(2026, 11, 30, 0, 5, 0, 0, wib)
	rep, err = svc.Report(context.Background(), PayrollKindWeek, mondayEarly)
	if err != nil {
		t.Fatalf("Report(Monday 00:05): %v", err)
	}
	if got := rep.Period.Start.In(wib).Format("2006-01-02"); got != "2026-11-30" {
		t.Errorf("week start for Monday 00:05 = %s, want 2026-11-30", got)
	}
	if start := rep.Period.Start.In(wib); mondayEarly.Before(start) {
		t.Errorf("Monday 00:05 WIB %v is before its own week start %v", mondayEarly, start)
	}
	previousEndExclusive := time.Date(2026, 11, 30, 0, 0, 0, 0, wib)
	if mondayEarly.Before(previousEndExclusive) {
		t.Errorf("Monday 00:05 WIB %v unexpectedly falls in the previous week", mondayEarly)
	}
}

// TestPayrollReportErrors pins that a broken read surfaces instead of being
// turned into an empty, plausible-looking payroll.
func TestPayrollReportErrors(t *testing.T) {
	svc := payrollService(&fakePayrollVisits{}, &fakePayrollRates{err: errors.New("boom")})
	visits := svc.visits.(*fakePayrollVisits)
	visits.visits = []repo.PayrollVisitRow{payrollVisit(uuid.New(), "Gita", time.Now(), 1_000, 0)}

	if _, err := svc.Report(context.Background(), PayrollKindMonth, time.Now()); err == nil {
		t.Fatal("Report swallowed a rate lookup error")
	}
	if _, err := svc.Report(context.Background(), "daily", time.Now()); err == nil {
		t.Fatal("Report accepted an unknown period kind")
	}
}
