package service

import (
	"context"
	"errors"
	"fmt"
	"sort"

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
	byStaff := map[string]int64{}
	for _, ln := range lines {
		sum.GrossIDR += ln.AmountIDR
		sum.Count++
		if ln.PaymentMethod == domain.PaymentMethodCash {
			sum.CashIDR += ln.AmountIDR
		} else {
			sum.QRISIDR += ln.AmountIDR
		}
		byService[ln.ServiceName] += ln.AmountIDR
		byStaff[ln.StaffName] += ln.AmountIDR
	}
	for _, rf := range refunds {
		sum.RefundIDR += rf.AmountIDR
		sum.RefundCnt++
	}
	sum.NetIDR = sum.GrossIDR - sum.RefundIDR
	sum.ServiceRows = sortedTotals(byService)
	sum.StaffRows = sortedTotals(byStaff)
	return sum, lines, refunds, nil
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
