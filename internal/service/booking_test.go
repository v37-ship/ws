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

func TestValidateGuest(t *testing.T) {
	name, phone, email, err := ValidateGuest("Sari", "0821-1347-294", "sari@example.com")
	if err != nil {
		t.Fatalf("valid guest = %v", err)
	}
	if name != "Sari" || phone != "08211347294" || email != "sari@example.com" {
		t.Errorf("normalized = %q %q %q", name, phone, email)
	}
	for _, tc := range []struct {
		name, phone, email string
	}{
		{"A", "08211347294", ""},
		{"Sari", "123", ""},
		{"Sari", "08211347294", "not-an-email"},
	} {
		if _, _, _, err := ValidateGuest(tc.name, tc.phone, tc.email); err == nil {
			t.Errorf("%v: expected error", tc)
		}
	}
	if _, _, _, err := ValidateGuest("Sari", "08211347294", ""); err != nil {
		t.Errorf("email optional = %v", err)
	}
}

func TestBookingTokenOK(t *testing.T) {
	tok, err := NewAccessToken()
	if err != nil {
		t.Fatalf("token gen = %v", err)
	}
	b := domain.Booking{AccessToken: &tok}
	if !b.TokenOK(tok) {
		t.Errorf("valid token rejected")
	}
	if b.TokenOK(tok+"x") || b.TokenOK("") {
		t.Errorf("bad token accepted")
	}
	if (domain.Booking{}).TokenOK(tok) {
		t.Errorf("nil token accepted")
	}
}

func TestBookingOwnedBy(t *testing.T) {
	id := uuid.New()
	b := domain.Booking{CustomerID: &id}
	if !b.OwnedBy(id) || b.OwnedBy(uuid.New()) {
		t.Errorf("ownership mismatch")
	}
	if (domain.Booking{}).OwnedBy(id) {
		t.Errorf("guest owned by account")
	}
}

func TestRunRedeemedCharge(t *testing.T) {
	errRedeem := errors.New("promo exhausted")
	errCharge := errors.New("qris charge")
	type calls struct{ redeem, charge, unredeem int }
	run := func(redeemErr, chargeErr error) (calls, error) {
		var c calls
		err := runRedeemedCharge(
			func() error { c.redeem++; return redeemErr },
			func() error { c.charge++; return chargeErr },
			func() error { c.unredeem++; return nil },
		)
		return c, err
	}

	t.Run("redeem fails: nothing else runs", func(t *testing.T) {
		c, err := run(errRedeem, nil)
		if !errors.Is(err, errRedeem) {
			t.Fatalf("err = %v, want errRedeem", err)
		}
		if c != (calls{redeem: 1}) {
			t.Fatalf("calls = %+v, want only redeem", c)
		}
	})
	t.Run("charge fails: redemption is rolled back", func(t *testing.T) {
		c, err := run(nil, errCharge)
		if !errors.Is(err, errCharge) {
			t.Fatalf("err = %v, want errCharge", err)
		}
		if c != (calls{redeem: 1, charge: 1, unredeem: 1}) {
			t.Fatalf("calls = %+v, want redeem+charge+unredeem", c)
		}
	})
	t.Run("both succeed: no rollback", func(t *testing.T) {
		c, err := run(nil, nil)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if c != (calls{redeem: 1, charge: 1}) {
			t.Fatalf("calls = %+v, want redeem+charge", c)
		}
	})
}
