package service

import (
	"testing"

	"spa-booking/internal/domain"
)

func TestNormalizePromoCode(t *testing.T) {
	for in, want := range map[string]string{
		"sehat10":    "SEHAT10",
		"  Hemat50 ": "HEMAT50",
		"":           "",
	} {
		if got := NormalizePromoCode(in); got != want {
			t.Errorf("%q = %q want %q", in, got, want)
		}
	}
}

func TestBookingNet(t *testing.T) {
	b := domain.Booking{PriceIDR: 250000, DiscountIDR: 25000}
	if got := b.Net(); got != 225000 {
		t.Errorf("net = %d want 225000", got)
	}
	b = domain.Booking{PriceIDR: 250000}
	if got := b.Net(); got != 250000 {
		t.Errorf("no-discount net = %d want 250000", got)
	}
	b = domain.Booking{PriceIDR: 10000, DiscountIDR: 50000}
	if got := b.Net(); got != 0 {
		t.Errorf("over-discount net = %d want 0", got)
	}
}
