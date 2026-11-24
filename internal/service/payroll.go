package service

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"

	"github.com/google/uuid"
)

// Payroll accrues commission per therapist over completed visits, payment-blind,
// and deducts refunds by the date they were recorded. The rules live here so the
// page and the mark-paid snapshot both read one calculation:
//
//   - a visit counts on its WIB service date; the booking's staff owns it;
//   - the base is the visit's frozen price (list) or price minus discount
//     (net), floored at zero per visit; the booking fee and transport fee never
//     count;
//   - a refund is attributable to its booking, capped per booking per period at
//     that booking's base under the row's commission_base; anything beyond the
//     cap is ignored, never carried;
//   - a rate row is resolved at the period's start; a therapist with work and no
//     rate is listed and flagged, never omitted;
//   - fixed pay follows its own cadence (R9): weekly fixed repeats for each
//     contained WIB week on the month view and is paid once on the week view,
//     while monthly fixed is paid once on the month view and omitted on the week
//     view;
//   - commission is round(net × percent_bp / 10000) to the nearest Rp 1,000,
//     half away from zero, so a late refund's negative period mirrors the
//     positive side.
//
// Money is integer IDR throughout: no float ever touches a payroll figure.

// PayrollKindWeek and PayrollKindMonth are the two page views. They are not the
// rate cadences (which reuse domain.PeriodWeekly/PeriodMonthly): a weekly-cadence
// fixed can appear on the month view, repeated.
const (
	PayrollKindWeek  = "week"
	PayrollKindMonth = "month"
)

// PayrollPeriod is a normalised WIB period. Start is the first day's midnight
// and End the last day's midnight in WIB (date-only); the SQL layer reads the
// half-open range [Start, End+1day).
type PayrollPeriod struct {
	Kind  string
	Start time.Time
	End   time.Time
}

// PayrollVisit is one completed visit as the page's expandable list shows it.
// PriceIDR/DiscountIDR are the booking's frozen snapshots; BaseIDR is what the
// row's resolved commission base actually counted (list or net), so the detail
// list never has to re-implement the base rule.
type PayrollVisit struct {
	BookingID    uuid.UUID
	StartAt      time.Time
	CustomerName string
	ServiceName  string
	PriceIDR     int64
	DiscountIDR  int64
	BaseIDR      int64
}

// PayrollRefundLine is one recorded refund on the row. AmountIDR is what was
// recorded — the lines are the audit trail, so they are not scaled to the cap;
// AppliedIDR is the capped deduction the period actually took (AmountIDR unless
// the booking's base under the resolved rate capped it).
type PayrollRefundLine struct {
	RefundID   uuid.UUID
	BookingID  uuid.UUID
	CreatedAt  time.Time
	AmountIDR  int64
	AppliedIDR int64
	Reason     string
}

// PayrollRow is one therapist's period. NetIDR = GrossIDR - RefundIDR and may be
// negative when a late refund lands in a period with no visits; TotalIDR =
// CommissionIDR + FixedIDR.
type PayrollRow struct {
	StaffID   uuid.UUID
	StaffName string
	Sessions  int
	// GrossIDR, RefundIDR and NetIDR are base-dependent money; NetIDR may go
	// negative.
	GrossIDR  int64
	RefundIDR int64
	NetIDR    int64
	// PercentBp, Base and Cadence are the resolved rate. Base and Cadence are
	// empty when the rate is missing (RateMissing), because no base or cadence
	// was ever chosen — the gross then uses the spec's net default so the
	// therapist is not silently priced at list.
	PercentBp int
	Base      string
	Cadence   string
	// CommissionIDR, FixedIDR and TotalIDR are the payable figures; all three
	// can be negative (a refund-only period) except FixedIDR.
	CommissionIDR int64
	FixedIDR      int64
	TotalIDR      int64
	// FixedOnMonth marks a monthly-cadence fixed that the week view omits
	// because it is paid once on the month view; the page says "counted on
	// the month" instead of showing an unexplained zero.
	FixedOnMonth bool
	// FixedOnly marks a row added for an active therapist whose resolved rate
	// has a fixed component and whose cadence matches the view, even though the
	// period holds no visits or refunds. The retainer is payable; the row's
	// commission and sessions are zero.
	FixedOnly   bool
	RateMissing bool
	Visits      []PayrollVisit
	RefundLines []PayrollRefundLine
}

// PayrollReport is the whole period. Rows are the union of therapists with
// completed visits and therapists with refunds recorded in the period, sorted
// by name then id. MissingRate counts flagged rows; the totals sum the rows.
type PayrollReport struct {
	Period      PayrollPeriod
	Rows        []PayrollRow
	MissingRate int

	TotalGrossIDR      int64
	TotalRefundIDR     int64
	TotalCommissionIDR int64
	TotalFixedIDR      int64
	TotalIDR           int64
}

// payrollPeriodReader is the period's two reads plus the roster the fixed-only
// rows come from (repo.PayrollRepo in production, a fake in the unit tables).
type payrollPeriodReader interface {
	CompletedVisits(ctx context.Context, from, to time.Time) ([]repo.PayrollVisitRow, error)
	RefundsInRange(ctx context.Context, from, to time.Time) ([]repo.PayrollRefundRow, error)
	ListStaff(ctx context.Context) ([]domain.Staff, error)
}

// payrollRateReader is the rate timeline's point read; absence is (nil, nil).
type payrollRateReader interface {
	EffectiveFor(ctx context.Context, staffID uuid.UUID, periodStart time.Time) (*domain.StaffComp, error)
}

// PayrollService computes period payroll. It is constructed by cmd/server and
// directly by its tests.
type PayrollService struct {
	visits payrollPeriodReader
	rates  payrollRateReader
}

func NewPayrollService(visits *repo.PayrollRepo, rates *repo.StaffCompRepo) *PayrollService {
	return &PayrollService{visits: visits, rates: rates}
}

// Report computes one period. It reads visits and refunds in two queries, then
// resolves each therapist's rate once, so a cancellation between the two reads
// cannot leave a row half-priced.
func (s *PayrollService) Report(ctx context.Context, kind string, start time.Time) (PayrollReport, error) {
	period, err := PayrollPeriodFor(kind, start)
	if err != nil {
		return PayrollReport{}, err
	}
	visits, err := s.visits.CompletedVisits(ctx, period.Start, period.End)
	if err != nil {
		return PayrollReport{}, fmt.Errorf("payroll visits: %w", err)
	}
	refunds, err := s.visits.RefundsInRange(ctx, period.Start, period.End)
	if err != nil {
		return PayrollReport{}, fmt.Errorf("payroll refunds: %w", err)
	}

	weeks := 0
	if period.Kind == PayrollKindMonth {
		weeks = weeksInMonth(period.Start)
	}

	// Register every therapist that has work or a refund; a refund-only row is
	// legal and can be negative. Refund rows are kept keyed by staff because the
	// cap needs their booking's price/discount, which the public line shape does
	// not carry.
	rows := make(map[uuid.UUID]*PayrollRow, len(visits)+len(refunds))
	refundsByStaff := make(map[uuid.UUID][]repo.PayrollRefundRow, len(refunds))
	getRow := func(id uuid.UUID, name string) *PayrollRow {
		row := rows[id]
		if row == nil {
			row = &PayrollRow{StaffID: id}
			rows[id] = row
		}
		if row.StaffName == "" {
			row.StaffName = name
		}
		return row
	}
	for _, v := range visits {
		row := getRow(v.StaffID, v.StaffName)
		row.Visits = append(row.Visits, PayrollVisit{
			BookingID:    v.BookingID,
			StartAt:      v.StartAt,
			CustomerName: v.CustomerName,
			ServiceName:  v.ServiceName,
			PriceIDR:     v.PriceIDR,
			DiscountIDR:  v.DiscountIDR,
		})
	}
	for _, rf := range refunds {
		getRow(rf.StaffID, rf.StaffName)
		refundsByStaff[rf.StaffID] = append(refundsByStaff[rf.StaffID], rf)
	}

	// R26: fixed pay is an entitlement, not visit-contingent. An active
	// therapist whose resolved rate carries a fixed component and whose cadence
	// matches the view is payable even with no visits or refunds in the period;
	// without this row the retainer would be silently unpayable. A therapist
	// with work already has a row above and is left untouched, and a
	// percent-only (or inactive) therapist is not added.
	staff, err := s.visits.ListStaff(ctx)
	if err != nil {
		return PayrollReport{}, fmt.Errorf("payroll staff: %w", err)
	}
	for _, st := range staff {
		if !st.Active {
			continue
		}
		if _, ok := rows[st.ID]; ok {
			continue
		}
		comp, err := s.rates.EffectiveFor(ctx, st.ID, period.Start)
		if err != nil {
			return PayrollReport{}, fmt.Errorf("payroll rate for %s: %w", st.ID, err)
		}
		if comp == nil || comp.FixedIDR <= 0 || !CadenceMatchesView(comp.Cadence, period.Kind) {
			continue
		}
		rows[st.ID] = &PayrollRow{StaffID: st.ID, StaffName: st.Name, FixedOnly: true}
	}

	report := PayrollReport{Period: period, Rows: make([]PayrollRow, 0, len(rows))}
	for id, row := range rows {
		comp, err := s.rates.EffectiveFor(ctx, id, period.Start)
		if err != nil {
			return PayrollReport{}, fmt.Errorf("payroll rate for %s: %w", id, err)
		}
		// The spec's default base is net. A missing rate row means no base was
		// chosen, so the gross (and the refund cap it feeds) still uses that
		// default while Base stays empty for the page's "rate not configured"
		// state.
		base := domain.CompBaseNet
		if comp == nil {
			row.RateMissing = true
			report.MissingRate++
		} else {
			row.PercentBp = comp.PercentBp
			row.Base = comp.CommissionBase
			row.Cadence = comp.Cadence
			base = comp.CommissionBase
			row.FixedIDR = resolveFixed(comp.FixedIDR, comp.Cadence, period.Kind, weeks)
			// The week view omits a monthly fixed by design; flag it so the
			// page can say why the column is empty instead of looking broken.
			row.FixedOnMonth = comp.FixedIDR > 0 &&
				comp.Cadence == domain.CompCadenceMonthly &&
				period.Kind == PayrollKindWeek
		}

		for i := range row.Visits {
			v := &row.Visits[i]
			v.BaseIDR = baseFor(v.PriceIDR, v.DiscountIDR, base)
			row.Sessions++
			row.GrossIDR += v.BaseIDR
		}

		// Per-booking spend inside this period: the cap is the booking's base
		// under the resolved base, and it is not carried across periods.
		spent := make(map[uuid.UUID]int64, len(refundsByStaff[id]))
		for _, rf := range refundsByStaff[id] {
			capIDR := baseFor(rf.PriceIDR, rf.DiscountIDR, base)
			remaining := capIDR - spent[rf.BookingID]
			if remaining < 0 {
				remaining = 0
			}
			applied := rf.AmountIDR
			if applied > remaining {
				applied = remaining
			}
			spent[rf.BookingID] += applied
			row.RefundIDR += applied
			row.RefundLines = append(row.RefundLines, PayrollRefundLine{
				RefundID:   rf.RefundID,
				BookingID:  rf.BookingID,
				CreatedAt:  rf.CreatedAt,
				AmountIDR:  rf.AmountIDR,
				AppliedIDR: applied,
				Reason:     rf.Reason,
			})
		}

		row.NetIDR = row.GrossIDR - row.RefundIDR
		row.CommissionIDR = commissionBp(row.NetIDR, row.PercentBp)
		row.TotalIDR = row.CommissionIDR + row.FixedIDR

		report.Rows = append(report.Rows, *row)
		report.TotalGrossIDR += row.GrossIDR
		report.TotalRefundIDR += row.RefundIDR
		report.TotalCommissionIDR += row.CommissionIDR
		report.TotalFixedIDR += row.FixedIDR
		report.TotalIDR += row.TotalIDR
	}

	sort.Slice(report.Rows, func(i, j int) bool {
		if report.Rows[i].StaffName != report.Rows[j].StaffName {
			return report.Rows[i].StaffName < report.Rows[j].StaffName
		}
		return bytes.Compare(report.Rows[i].StaffID[:], report.Rows[j].StaffID[:]) < 0
	})
	return report, nil
}

// CadenceMatchesView reports whether a rate cadence settles on this page view:
// weekly staff settle only on week views, monthly staff only on month views
// (R27). It is the one source of truth for both the pay control and the pay
// handler's refusal, because a payout keyed by the other kind would never see
// this one and the same visits could be paid twice.
func CadenceMatchesView(cadence, kind string) bool {
	switch kind {
	case PayrollKindWeek:
		return cadence == domain.CompCadenceWeekly
	case PayrollKindMonth:
		return cadence == domain.CompCadenceMonthly
	}
	return false
}

// PayrollWeeksInMonth exposes the contained-WIB-week count the month view uses
// for a weekly cadence's fixed and its "n/m weeks paid" note.
func PayrollWeeksInMonth(start time.Time) int { return weeksInMonth(start) }

// PayrollPeriodFor normalises any instant to the WIB week (Monday–Sunday) or
// month containing it. Start is that day's midnight and End the last day's
// midnight, both in WIB; the SQL window is [Start, End+1day).
func PayrollPeriodFor(kind string, start time.Time) (PayrollPeriod, error) {
	t := start.In(wib)
	switch kind {
	case PayrollKindWeek:
		// Offset from Monday: Sunday (0) is 6 days after Monday, Monday (1) is 0.
		offset := (int(t.Weekday()) + 6) % 7
		y, m, d := t.Date()
		monday := time.Date(y, m, d, 0, 0, 0, 0, wib).AddDate(0, 0, -offset)
		return PayrollPeriod{Kind: kind, Start: monday, End: monday.AddDate(0, 0, 6)}, nil
	case PayrollKindMonth:
		y, m, _ := t.Date()
		first := time.Date(y, m, 1, 0, 0, 0, 0, wib)
		return PayrollPeriod{Kind: kind, Start: first, End: first.AddDate(0, 1, -1)}, nil
	default:
		return PayrollPeriod{}, fmt.Errorf("unknown payroll period kind %q", kind)
	}
}

// commissionBp is round(net × bp / 10000) to the nearest Rp 1,000, half away
// from zero. The integer form compares the product against half of
// 10000 × 1000 = 10,000,000, so no float ever rounds a salary.
func commissionBp(netIDR int64, bp int) int64 {
	if netIDR == 0 || bp <= 0 {
		return 0
	}
	return divRound(netIDR*int64(bp), 10_000*1_000) * 1_000
}

// baseFor applies the rate's commission base to one visit: list is the price,
// net is price minus discount floored at zero. An unconfigured base falls back
// to the spec default (net).
func baseFor(priceIDR, discountIDR int64, base string) int64 {
	if base == domain.CompBaseList {
		return priceIDR
	}
	net := priceIDR - discountIDR
	if net < 0 {
		return 0
	}
	return net
}

// resolveFixed applies R9 to one resolved rate: a weekly fixed is paid once on
// the week view and repeated for each contained WIB week on the month view, a
// monthly fixed once on the month view and omitted on the week view.
func resolveFixed(fixedIDR int64, cadence, kind string, weeks int) int64 {
	if fixedIDR <= 0 {
		return 0
	}
	switch cadence {
	case domain.CompCadenceWeekly:
		switch kind {
		case PayrollKindWeek:
			return fixedIDR
		case PayrollKindMonth:
			if weeks <= 0 {
				return 0
			}
			return fixedIDR * int64(weeks)
		}
	case domain.CompCadenceMonthly:
		if kind == PayrollKindMonth {
			return fixedIDR
		}
	}
	return 0
}

// weeksInMonth counts the WIB Mondays in the month containing start — the
// "contained weeks" a weekly-cadence fixed is paid for on the month view.
func weeksInMonth(start time.Time) int {
	t := start.In(wib)
	y, m, _ := t.Date()
	first := time.Date(y, m, 1, 0, 0, 0, 0, wib)
	last := first.AddDate(0, 1, -1)
	// Days from the first of the month to its first Monday: Sunday is 1 day
	// ahead, Monday itself 0, Tuesday 6.
	offset := (8 - int(first.Weekday())) % 7
	count := 0
	for d := first.AddDate(0, 0, offset); !d.After(last); d = d.AddDate(0, 0, 7) {
		count++
	}
	return count
}
