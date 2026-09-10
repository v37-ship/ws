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
