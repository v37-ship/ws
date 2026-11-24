package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"

	"github.com/google/uuid"
)

// ReportService builds monthly revenue reports and records refunds.
type ReportService struct {
	reports  *repo.ReportRepo
	bookings *repo.BookingRepo
	payments *repo.PaymentRepo
}

func NewReportService(reports *repo.ReportRepo, bookings *repo.BookingRepo, payments *repo.PaymentRepo) *ReportService {
	return &ReportService{reports: reports, bookings: bookings, payments: payments}
}

// MonthlyReport aggregates paid payments (by paid_at month) and refunds
// (by recorded month). Net = Gross - Refunds (cash basis, Asia/Jakarta).
func (s *ReportService) MonthlyReport(ctx context.Context, month string, locationID uuid.UUID) (domain.ReportSummary, []domain.ReportLine, []domain.ReportRefund, error) {
	return s.monthlyReport(ctx, month, locationID, 0)
}

// MonthlyReportThrough is the month-to-date read the delta line compares: the
// same cash-basis aggregation as MonthlyReport, cut at throughDay (a WIB
// day-of-month, inclusive) on both the payment and refund sides. throughDay <= 0
// means the whole month, so a caller can use one path either way — but the
// uncut MonthlyReport keeps calling the unfiltered queries, so the existing
// page numbers never gain a predicate.
func (s *ReportService) MonthlyReportThrough(ctx context.Context, month string, locationID uuid.UUID, throughDay int) (domain.ReportSummary, error) {
	sum, _, _, err := s.monthlyReport(ctx, month, locationID, throughDay)
	return sum, err
}

func (s *ReportService) monthlyReport(ctx context.Context, month string, locationID uuid.UUID, throughDay int) (domain.ReportSummary, []domain.ReportLine, []domain.ReportRefund, error) {
	var (
		lines   []domain.ReportLine
		refunds []domain.ReportRefund
		err     error
	)
	if throughDay > 0 {
		lines, err = s.reports.MonthlyLinesThrough(ctx, month, locationID, throughDay)
	} else {
		lines, err = s.reports.MonthlyLines(ctx, month, locationID)
	}
	if err != nil {
		return domain.ReportSummary{}, nil, nil, err
	}
	if throughDay > 0 {
		refunds, err = s.reports.MonthlyRefundsThrough(ctx, month, locationID, throughDay)
	} else {
		refunds, err = s.reports.MonthlyRefunds(ctx, month, locationID)
	}
	if err != nil {
		return domain.ReportSummary{}, nil, nil, err
	}
	sum := domain.ReportSummary{Month: month}
	byService := map[string]int64{}
	staffAgg := map[uuid.UUID]*staffAccum{}
	staffOrder := map[uuid.UUID]string{}
	for _, ln := range lines {
		sum.GrossIDR += ln.AmountIDR
		sum.Count++
		if ln.PaymentMethod == domain.PaymentMethodCash {
			sum.CashIDR += ln.AmountIDR
		} else {
			sum.QRISIDR += ln.AmountIDR
		}
		byService[ln.ServiceName] += ln.AmountIDR
		acc := staffAgg[ln.StaffID]
		if acc == nil {
			acc = &staffAccum{customers: map[uuid.UUID]struct{}{}, services: map[string]*domain.StaffServiceRow{}}
			staffAgg[ln.StaffID] = acc
		}
		acc.sessions++
		acc.gross += ln.AmountIDR
		if ln.CustomerID != nil {
			acc.customers[*ln.CustomerID] = struct{}{}
		}
		svc := acc.services[ln.ServiceName]
		if svc == nil {
			svc = &domain.StaffServiceRow{Name: ln.ServiceName}
			acc.services[ln.ServiceName] = svc
		}
		svc.Count++
		svc.Total += ln.AmountIDR
		staffOrder[ln.StaffID] = ln.StaffName
	}
	for _, rf := range refunds {
		sum.RefundIDR += rf.AmountIDR
		sum.RefundCnt++
		acc := staffAgg[rf.StaffID]
		if acc == nil {
			acc = &staffAccum{customers: map[uuid.UUID]struct{}{}, services: map[string]*domain.StaffServiceRow{}}
			staffAgg[rf.StaffID] = acc
		}
		acc.refund += rf.AmountIDR
		if _, ok := staffOrder[rf.StaffID]; !ok || staffOrder[rf.StaffID] == "" {
			staffOrder[rf.StaffID] = rf.StaffName
		}
	}
	sum.NetIDR = sum.GrossIDR - sum.RefundIDR
	sum.ServiceRows = sortedTotals(byService)
	sum.StaffRows = buildStaffRows(staffAgg, staffOrder)
	attachStaffLines(sum.StaffRows, lines)
	attachStaffRefunds(sum.StaffRows, refunds)
	return sum, lines, refunds, nil
}

// DemandReport reads the month's business-health block. GrossPaid is passed in
// from the caller's MonthlyReport summary — the same cash-basis figure the
// sticky strip shows — so the demand read never re-derives revenue and never
// pays for a second payments query. The two ratios are computed here, from the
// raw counts, because the repo's job is rows, not percentages.
func (s *ReportService) DemandReport(ctx context.Context, month string, locationID uuid.UUID, grossPaidIDR int64) (domain.ReportDemand, error) {
	d, err := s.reports.DemandCounts(ctx, month, locationID)
	if err != nil {
		return domain.ReportDemand{}, err
	}
	d.GrossPaid = grossPaidIDR
	d.HomecareShareBp = demandRateBp(d.HomecareVisits, d.Visits)
	d.NoShowRateBp = demandRateBp(d.NoShowVisits, d.TerminalVisits)
	return d, nil
}

// demandRateBp expresses part/whole as basis points (1 bp = 0.01%), rounded
// half up. A zero (or negative) denominator yields 0: "0 of 0" has no rate, and
// the template renders "—" for it rather than a silent 0%.
func demandRateBp(part, whole int) int {
	if part <= 0 || whole <= 0 {
		return 0
	}
	return (part*10000 + whole/2) / whole
}

// DailyReport is the Daily tab's whole read: the WIB day's visit mix, the money
// collected that day (by paid_at) and today's remaining confirmed rows. date is
// expected already normalized by the handler (YYYY-MM-DD, not in the future);
// the next-day/upcoming window still uses the real clock.
func (s *ReportService) DailyReport(ctx context.Context, date string, locationID uuid.UUID) (domain.DailySummary, error) {
	sum, err := s.reports.DailyCounts(ctx, date, locationID)
	if err != nil {
		return domain.DailySummary{}, err
	}
	if sum.CashIDR, sum.QRISIDR, err = s.reports.DailyPayments(ctx, date, locationID); err != nil {
		return domain.DailySummary{}, err
	}
	if sum.Upcoming, err = s.reports.DailyUpcoming(ctx, date, time.Now(), locationID); err != nil {
		return domain.DailySummary{}, err
	}
	return sum, nil
}

// attachStaffLines files each paid line under its staff row (lines arrive
// paid-date ordered from the repo, so per-staff order is preserved).
func attachStaffLines(rows []domain.StaffRow, lines []domain.ReportLine) {
	byStaff := map[uuid.UUID][]domain.ReportLine{}
	for _, ln := range lines {
		byStaff[ln.StaffID] = append(byStaff[ln.StaffID], ln)
	}
	for i := range rows {
		rows[i].Lines = byStaff[rows[i].StaffID]
	}
}

// attachStaffRefunds files each recorded refund under its staff row.
func attachStaffRefunds(rows []domain.StaffRow, refunds []domain.ReportRefund) {
	byStaff := map[uuid.UUID][]domain.ReportRefund{}
	for _, rf := range refunds {
		byStaff[rf.StaffID] = append(byStaff[rf.StaffID], rf)
	}
	for i := range rows {
		rows[i].Refunds = byStaff[rows[i].StaffID]
	}
}

// staffAccum gathers per-staff detail before flattening into domain.StaffRow.
type staffAccum struct {
	sessions  int
	gross     int64
	refund    int64
	customers map[uuid.UUID]struct{}
	services  map[string]*domain.StaffServiceRow
}

// buildStaffRows flattens per-staff accumulators into desc-sorted rows (by net,
// then gross, then name). Avg = gross / sessions. Pure function — unit-testable.
func buildStaffRows(staffAgg map[uuid.UUID]*staffAccum, staffOrder map[uuid.UUID]string) []domain.StaffRow {
	rows := make([]domain.StaffRow, 0, len(staffAgg))
	for id, acc := range staffAgg {
		row := domain.StaffRow{
			StaffID:         id,
			Name:            staffOrder[id],
			Sessions:        acc.sessions,
			UniqueCustomers: len(acc.customers),
			GrossIDR:        acc.gross,
			RefundIDR:       acc.refund,
		}
		row.NetIDR = row.GrossIDR - row.RefundIDR
		if row.Sessions > 0 {
			row.AvgIDR = row.GrossIDR / int64(row.Sessions)
		}
		svcs := make([]domain.StaffServiceRow, 0, len(acc.services))
		for _, s := range acc.services {
			svcs = append(svcs, *s)
		}
		sort.Slice(svcs, func(i, j int) bool {
			if svcs[i].Count == svcs[j].Count {
				if svcs[i].Total == svcs[j].Total {
					return svcs[i].Name < svcs[j].Name
				}
				return svcs[i].Total > svcs[j].Total
			}
			return svcs[i].Count > svcs[j].Count
		})
		row.Services = svcs
		if len(svcs) > 0 {
			row.TopService = svcs[0].Name
		}
		if len(svcs) > 3 {
			row.TopServices = svcs[:3]
			row.MoreCount = len(svcs) - 3
		} else {
			row.TopServices = svcs
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].NetIDR == rows[j].NetIDR {
			if rows[i].GrossIDR == rows[j].GrossIDR {
				return rows[i].Name < rows[j].Name
			}
			return rows[i].GrossIDR > rows[j].GrossIDR
		}
		return rows[i].NetIDR > rows[j].NetIDR
	})
	return rows
}

// sortedTotals flattens a name->total map into desc-sorted rows.
func sortedTotals(m map[string]int64) []domain.NameTotal {
	rows := make([]domain.NameTotal, 0, len(m))
	for name, total := range m {
		rows = append(rows, domain.NameTotal{Name: name, Total: total})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Total == rows[j].Total {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Total > rows[j].Total
	})
	return rows
}

// shiftMonth moves a YYYY-MM month by delta months; invalid input yields "".
func shiftMonth(month string, delta int) string {
	t, err := time.Parse("2006-01", month)
	if err != nil {
		return ""
	}
	return t.AddDate(0, delta, 0).Format("2006-01")
}

// PrevMonth returns the month before the given YYYY-MM ("" if invalid).
func PrevMonth(month string) string { return shiftMonth(month, -1) }

// NextMonth returns the month after the given YYYY-MM ("" if invalid).
func NextMonth(month string) string { return shiftMonth(month, 1) }

// MonthlyNetSeries returns the cash-basis net for `months` consecutive months
// ending at endMonth, oldest first — the sparkline's inputs. It deliberately
// loops MonthlyReport rather than issuing one generate_series query: six
// single-month reads are cheap at this volume, and reusing the one monthly
// aggregation is what guarantees the chart's definition of net can never drift
// from the ledger's (gross by paid_at month, refunds by recorded month, WIB).
func (s *ReportService) MonthlyNetSeries(ctx context.Context, endMonth string, locationID uuid.UUID, months int) ([]domain.MonthNet, error) {
	if months <= 0 {
		months = 6
	}
	out := make([]domain.MonthNet, months)
	month := endMonth
	for i := months - 1; i >= 0; i-- {
		if month == "" {
			return nil, fmt.Errorf("invalid end month %q", endMonth)
		}
		sum, _, _, err := s.monthlyReport(ctx, month, locationID, 0)
		if err != nil {
			return nil, err
		}
		out[i] = domain.MonthNet{Month: month, NetIDR: sum.NetIDR}
		month = PrevMonth(month)
	}
	return out, nil
}

// MonthDelta compares the current month-to-date net with the same day-of-month
// last month, in basis points of a percent (+5000 = +50.0%, -2000 = -20.0%).
// The caller passes summaries that already cover the same cutoff (the current
// month's page summary is month-to-date by construction; the previous month is
// read through MonthlyReportThrough). A previous month with a non-positive net
// has no meaningful percentage baseline, so ok is false and the page renders
// its "first days of the month" state instead of a 100%-style artefact.
//
// asOf is the canonical WIB date the comparison runs through (YYYY-MM-DD),
// clamped to the month's real length; the page renders it as "per <day>
// <month>" in the request language.
func MonthDelta(current, previous domain.ReportSummary, dayOfMonth int) (pctBp int, asOf string, ok bool) {
	asOf = reportAsOf(current.Month, dayOfMonth)
	if previous.NetIDR <= 0 {
		return 0, asOf, false
	}
	delta := current.NetIDR - previous.NetIDR
	return int(divRound(delta*10000, previous.NetIDR)), asOf, true
}

// reportAsOf builds the cutoff date for a YYYY-MM month and a day-of-month.
// Invalid months yield ""; a day outside the month clamps to its first/last day
// (a client can only supply a real current day, but a fixture must not be able
// to mint an invalid date).
func reportAsOf(month string, day int) string {
	t, err := time.Parse("2006-01", month)
	if err != nil {
		return ""
	}
	last := time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, time.UTC).Day()
	if day < 1 {
		day = 1
	}
	if day > last {
		day = last
	}
	return fmt.Sprintf("%04d-%02d-%02d", t.Year(), int(t.Month()), day)
}

// divRound is integer division rounded half away from zero, with a positive
// denominator. It keeps the whole trend layer in integers: no floats means no
// NaN/Inf can ever reach the page.
func divRound(num, den int64) int64 {
	if den <= 0 {
		return 0
	}
	if num >= 0 {
		return (num + den/2) / den
	}
	return -((-num + den/2) / den)
}

// SparklineSVG renders a dependency-free inline sparkline for the monthly net
// series. It is pure: integer-only normalisation (max → top padding, min →
// bottom padding, an all-equal series → the vertical middle, an empty or
// all-zero series → the zero baseline), and it returns a string rather than
// template.HTML because the service must not import html/template.
//
// The returned markup is safe to wrap in template.HTML: every interpolated
// value is an int64 or an int (coordinates and dimensions computed here), the
// attribute set is fixed, and no caller-supplied text ever reaches the string.
// Non-positive width/height fall back to 320x64 so a fixture or a future caller
// cannot emit a degenerate viewBox.
func SparklineSVG(values []int64, width, height int) string {
	const pad = 2
	if width <= 0 {
		width = 320
	}
	if height <= 0 {
		height = 64
	}
	if width < 2*pad+2 {
		width = 2*pad + 2
	}
	if height < 2*pad+2 {
		height = 2*pad + 2
	}
	usableW, usableH := width-2*pad, height-2*pad

	points := make([][2]int, 0, len(values))
	if len(values) == 0 || allZero(values) {
		// Zero reads as the floor: a flat baseline at the bottom.
		points = append(points, [2]int{pad, height - pad}, [2]int{width - pad, height - pad})
	} else {
		min, max := values[0], values[0]
		for _, v := range values[1:] {
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
		}
		if max == min {
			y := pad + usableH/2
			if len(values) == 1 {
				points = append(points, [2]int{pad, y}, [2]int{width - pad, y})
			} else {
				for i := range values {
					points = append(points, [2]int{sparkX(i, len(values), pad, usableW), y})
				}
			}
		} else {
			span := max - min
			for i, v := range values {
				x := sparkX(i, len(values), pad, usableW)
				offset := int(divRound((v-min)*int64(usableH), span))
				points = append(points, [2]int{x, pad + usableH - offset})
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="spark" viewBox="0 0 %d %d" width="%d" height="%d" aria-hidden="true"><polyline points="`,
		width, height, width, height)
	for i, p := range points {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%d,%d", p[0], p[1])
	}
	b.WriteString(`" fill="none" stroke="currentColor" stroke-width="2" stroke-linejoin="round" stroke-linecap="round"/></svg>`)
	return b.String()
}

// sparkX spreads point i across the usable width. A single point is centred.
func sparkX(i, n, pad, usableW int) int {
	if n <= 1 {
		return pad + usableW/2
	}
	return pad + int(divRound(int64(i)*int64(usableW), int64(n-1)))
}

func allZero(values []int64) bool {
	for _, v := range values {
		if v != 0 {
			return false
		}
	}
	return true
}

// AvailableMonths lists YYYY-MM months (desc) with report data for the
// jump-to-month dropdown.
func (s *ReportService) AvailableMonths(ctx context.Context, locationID uuid.UUID) ([]string, error) {
	return s.reports.AvailableMonths(ctx, locationID, 24)
}

// refundAmountError rejects a non-positive refund amount. It runs before any
// payment is loaded, so a bad amount fails fast and deterministically.
func refundAmountError(amount int64) error {
	if amount <= 0 {
		return fmt.Errorf("%w: refund amount must be positive", ErrValidation)
	}
	return nil
}

// refundCeilingError enforces the refund money rule: a booking may never be
// refunded beyond what it actually paid. alreadyRefunded is trusted as given —
// the caller passes RefundedTotal, which already excludes voided refund rows.
func refundCeilingError(amount, paid, alreadyRefunded int64) error {
	if alreadyRefunded+amount > paid {
		return fmt.Errorf("%w: refund exceeds paid amount (paid %d, refunded %d)",
			ErrValidation, paid, alreadyRefunded)
	}
	return nil
}

// RecordRefund validates and stores a refund, plus an audit log entry.
// A booking can never be refunded more than it actually paid.
func (s *ReportService) RecordRefund(ctx context.Context, bookingID uuid.UUID, amount int64, reason string, actorID *uuid.UUID) (*domain.Refund, error) {
	if err := refundAmountError(amount); err != nil {
		return nil, err
	}
	p, err := s.payments.ByBookingID(ctx, bookingID)
	if err != nil {
		return nil, err
	}
	if p.Status != domain.PaymentPaid {
		return nil, fmt.Errorf("%w: booking has no paid payment to refund", ErrValidation)
	}
	refunded, err := s.reports.RefundedTotal(ctx, bookingID)
	if err != nil {
		return nil, err
	}
	if err := refundCeilingError(amount, p.AmountIDR, refunded); err != nil {
		return nil, err
	}
	rf, err := s.reports.CreateRefund(ctx, bookingID, amount, reason, actorID)
	if err != nil {
		return nil, err
	}
	_ = s.bookings.LogActivity(ctx, bookingID, actorID,
		fmt.Sprintf("Refund recorded Rp %d", amount), reason)
	return rf, nil
}

// VoidRefund voids a mistyped refund (admin correction). The row stays as
// the correction trail; totals exclude it from that moment on. Re-record
// the correct amount with RecordRefund afterwards.
func (s *ReportService) VoidRefund(ctx context.Context, refundID uuid.UUID, reason string, actorID *uuid.UUID) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("%w: void reason is required", ErrValidation)
	}
	rf, err := s.reports.RefundByID(ctx, refundID)
	if err != nil {
		return err
	}
	if rf.IsVoid() {
		return fmt.Errorf("%w: refund already voided", ErrValidation)
	}
	if err := s.reports.VoidRefund(ctx, refundID, actorID, reason); err != nil {
		return err
	}
	_ = s.bookings.LogActivity(ctx, rf.BookingID, actorID,
		fmt.Sprintf("Refund Rp %d voided", rf.AmountIDR), reason)
	return nil
}

// BookingRefunds returns a booking's payment (if any), its refunds, and total refunded.
func (s *ReportService) BookingRefunds(ctx context.Context, bookingID uuid.UUID) (*domain.Payment, []domain.Refund, int64, error) {
	p, err := s.payments.ByBookingID(ctx, bookingID)
	if errors.Is(err, repo.ErrNotFound) {
		p = nil // booking may legitimately have no payment row yet
	} else if err != nil {
		return nil, nil, 0, err
	}
	refunds, err := s.reports.RefundsForBooking(ctx, bookingID)
	if err != nil {
		return p, nil, 0, err
	}
	var total int64
	for _, rf := range refunds {
		total += rf.AmountIDR
	}
	return p, refunds, total, nil
}
