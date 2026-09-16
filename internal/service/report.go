package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
	lines, err := s.reports.MonthlyLines(ctx, month, locationID)
	if err != nil {
		return domain.ReportSummary{}, nil, nil, err
	}
	refunds, err := s.reports.MonthlyRefunds(ctx, month, locationID)
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
		acc.customers[ln.CustomerID] = struct{}{}
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
	return sum, lines, refunds, nil
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

// AvailableMonths lists YYYY-MM months (desc) with report data for the
// jump-to-month dropdown.
func (s *ReportService) AvailableMonths(ctx context.Context, locationID uuid.UUID) ([]string, error) {
	return s.reports.AvailableMonths(ctx, locationID, 24)
}

// RecordRefund validates and stores a refund, plus an audit log entry.
// A booking can never be refunded more than it actually paid.
func (s *ReportService) RecordRefund(ctx context.Context, bookingID uuid.UUID, amount int64, reason string, actorID *uuid.UUID) (*domain.Refund, error) {
	if amount <= 0 {
		return nil, fmt.Errorf("%w: refund amount must be positive", ErrValidation)
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
	if refunded+amount > p.AmountIDR {
		return nil, fmt.Errorf("%w: refund exceeds paid amount (paid %d, refunded %d)",
			ErrValidation, p.AmountIDR, refunded)
	}
	rf, err := s.reports.CreateRefund(ctx, bookingID, amount, reason, actorID)
	if err != nil {
		return nil, err
	}
	_ = s.bookings.LogActivity(ctx, bookingID, actorID,
		fmt.Sprintf("Refund recorded Rp %d", amount), reason)
	return rf, nil
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
