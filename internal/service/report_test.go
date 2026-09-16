package service

import (
	"testing"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
)

func TestPrevNextMonth(t *testing.T) {
	cases := []struct{ in, prev, next string }{
		{"2026-09", "2026-08", "2026-10"},
		{"2026-01", "2025-12", "2026-02"},
		{"2026-12", "2026-11", "2027-01"},
		{"nope", "", ""},
	}
	for _, c := range cases {
		if got := PrevMonth(c.in); got != c.prev {
			t.Errorf("PrevMonth(%q) = %q, want %q", c.in, got, c.prev)
		}
		if got := NextMonth(c.in); got != c.next {
			t.Errorf("NextMonth(%q) = %q, want %q", c.in, got, c.next)
		}
	}
}
func TestBuildStaffRowsSessionsUniqueServices(t *testing.T) {
	staffA := uuid.New()
	staffB := uuid.New()
	cust1 := uuid.New()
	cust2 := uuid.New()
	lines := []domain.ReportLine{
		{StaffID: staffA, StaffName: "Bidan Ayu", CustomerID: cust1, ServiceName: "Baby Massage", AmountIDR: 100000},
		{StaffID: staffA, StaffName: "Bidan Ayu", CustomerID: cust1, ServiceName: "Baby Massage", AmountIDR: 100000},
		{StaffID: staffA, StaffName: "Bidan Ayu", CustomerID: cust2, ServiceName: "Mom Massage", AmountIDR: 200000},
		{StaffID: staffB, StaffName: "Terapis Rina", CustomerID: cust2, ServiceName: "Facial", AmountIDR: 150000},
	}
	refunds := []domain.ReportRefund{
		{Refund: domain.Refund{AmountIDR: 50000}, StaffID: staffA, StaffName: "Bidan Ayu"},
	}
	// Build via the same accum path MonthlyReport uses.
	agg := map[uuid.UUID]*staffAccum{}
	order := map[uuid.UUID]string{}
	for _, ln := range lines {
		a := agg[ln.StaffID]
		if a == nil {
			a = &staffAccum{customers: map[uuid.UUID]struct{}{}, services: map[string]*domain.StaffServiceRow{}}
			agg[ln.StaffID] = a
		}
		a.sessions++
		a.gross += ln.AmountIDR
		a.customers[ln.CustomerID] = struct{}{}
		s := a.services[ln.ServiceName]
		if s == nil {
			s = &domain.StaffServiceRow{Name: ln.ServiceName}
			a.services[ln.ServiceName] = s
		}
		s.Count++
		s.Total += ln.AmountIDR
		order[ln.StaffID] = ln.StaffName
	}
	for _, rf := range refunds {
		a := agg[rf.StaffID]
		if a == nil {
			a = &staffAccum{customers: map[uuid.UUID]struct{}{}, services: map[string]*domain.StaffServiceRow{}}
			agg[rf.StaffID] = a
		}
		a.refund += rf.AmountIDR
		if _, ok := order[rf.StaffID]; !ok {
			order[rf.StaffID] = rf.StaffName
		}
	}
	rows := buildStaffRows(agg, order)
	if len(rows) != 2 {
		t.Fatalf("expected 2 staff rows, got %d", len(rows))
	}
	// Staff A nets 350k (400k gross - 50k refund), staff B nets 150k → A first.
	a := rows[0]
	if a.Name != "Bidan Ayu" {
		t.Errorf("expected Bidan Ayu first, got %q", a.Name)
	}
	if a.Sessions != 3 {
		t.Errorf("expected 3 sessions, got %d", a.Sessions)
	}
	if a.UniqueCustomers != 2 {
		t.Errorf("expected 2 unique customers, got %d", a.UniqueCustomers)
	}
	if a.GrossIDR != 400000 || a.RefundIDR != 50000 || a.NetIDR != 350000 {
		t.Errorf("unexpected money: gross=%d refund=%d net=%d", a.GrossIDR, a.RefundIDR, a.NetIDR)
	}
	if a.AvgIDR != 400000/3 {
		t.Errorf("unexpected avg=%d", a.AvgIDR)
	}
	if a.TopService != "Baby Massage" {
		t.Errorf("expected Baby Massage top, got %q", a.TopService)
	}
	if len(a.Services) != 2 || a.Services[0].Count != 2 {
		t.Errorf("unexpected service breakdown: %+v", a.Services)
	}
}
