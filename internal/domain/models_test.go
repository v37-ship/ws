package domain

import "testing"

func TestRoleStaff(t *testing.T) {
	if !RoleAdmin.IsStaff() {
		t.Errorf("expected RoleAdmin to be staff")
	}
	if !RoleCS.IsStaff() {
		t.Errorf("expected RoleCS to be staff")
	}
	if RoleCustomer.IsStaff() {
		t.Errorf("expected RoleCustomer not to be staff")
	}
}

func TestBookingStatusDisplay(t *testing.T) {
	tests := []struct {
		status   BookingStatus
		expected string
	}{
		{BookingConfirmed, "Confirmed"},
		{BookingCheckedIn, "Checked In"},
		{BookingInService, "In Service"},
		{BookingCompleted, "Completed"},
		{BookingNoShow, "No Show"},
		{BookingCancelled, "Cancelled"},
	}
	for _, tc := range tests {
		if tc.status.Display() != tc.expected {
			t.Errorf("expected %s, got %s", tc.expected, tc.status.Display())
		}
	}
}

func TestServiceDisplayName(t *testing.T) {
	s := Service{Name: "Baby Spa Soft", NameID: "Baby Spa Lembut", Description: "Gentle care", DescriptionID: "Perawatan lembut"}
	if got := s.DisplayName("id"); got != "Baby Spa Lembut" {
		t.Errorf("expected ID name, got %q", got)
	}
	if got := s.DisplayDescription("id"); got != "Perawatan lembut" {
		t.Errorf("expected ID description, got %q", got)
	}
	if got := s.DisplayName("en"); got != "Baby Spa Soft" {
		t.Errorf("expected canonical name, got %q", got)
	}
	empty := Service{Name: "Facial Massage", Description: "Massage"}
	if got := empty.DisplayName("id"); got != "Facial Massage" {
		t.Errorf("expected fallback name, got %q", got)
	}
	if got := empty.DisplayDescription("id"); got != "Massage" {
		t.Errorf("expected fallback description, got %q", got)
	}
}
