package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
)

func TestCreateStaffBooking_CashPaymentRule(t *testing.T) {
	svc := &BookingService{}

	// Cash attempted for booking_fee -> MUST fail with ErrValidation
	_, _, err := svc.CreateStaffBooking(context.Background(), StaffBookingRequest{
		CustomerID:     uuid.New(),
		LocationID:     uuid.New(),
		ServiceID:      uuid.New(),
		StaffID:        uuid.New(),
		StartAt:        time.Now(),
		PaymentMethod:  domain.PaymentMethodCash,
		PaymentPurpose: domain.PaymentPurposeBookingFee,
	})

	if err == nil {
		t.Fatalf("expected error when paying booking fee with cash, got nil")
	}
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation, got %v", err)
	}
}

func TestRescheduleRejectsPast(t *testing.T) {
	svc := &BookingService{}
	err := svc.Reschedule(context.Background(), uuid.New(), uuid.New(),
		time.Now().Add(-time.Hour), nil, "oops")
	if err == nil {
		t.Fatalf("expected error rescheduling to the past, got nil")
	}
	if !errors.Is(err, ErrValidation) {
		t.Errorf("expected ErrValidation, got %v", err)
	}
}

func TestNormalizePlace(t *testing.T) {
	cases := []struct {
		name      string
		place     domain.Place
		addr      string
		wantPlace domain.Place
		wantAddr  string
		wantErr   bool
	}{
		{"empty defaults onsite", "", "", domain.PlaceOnsite, "", false},
		{"onsite drops address", domain.PlaceOnsite, "  Jl. Mawar 1  ", domain.PlaceOnsite, "", false},
		{"homecare keeps trimmed address", domain.PlaceHomecare, "  Jl. Mawar No. 10, Jakarta  ", domain.PlaceHomecare, "Jl. Mawar No. 10, Jakarta", false},
		{"homecare empty address rejected", domain.PlaceHomecare, "   ", "", "", true},
		{"homecare short address rejected", domain.PlaceHomecare, "Jl. A", "", "", true},
		{"unknown place rejected", domain.Place("villa"), "Jl. Mawar No. 10, Jakarta", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotPlace, gotAddr, err := normalizePlace(c.place, c.addr)
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if !errors.Is(err, ErrValidation) {
					t.Errorf("expected ErrValidation, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotPlace != c.wantPlace || gotAddr != c.wantAddr {
				t.Errorf("got (%q,%q), want (%q,%q)", gotPlace, gotAddr, c.wantPlace, c.wantAddr)
			}
		})
	}
}
