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
