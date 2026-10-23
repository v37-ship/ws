package whatsapp

import (
	"strings"
	"testing"
)

func TestRenderBookingConfirm(t *testing.T) {
	body, err := Render("booking_confirm", Payload{
		"name": "Bunda Sari", "code": "SPA-123", "service": "Baby Spa",
		"when": "Senin, 26 Oktober 2026 · 14:00 WIB", "staff": "Bidan Ayu",
		"location": "Kayu Putih", "amount": float64(150000), "paid": false,
		"pay_url": "https://wangga.biz.id/bookings/x/pay",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"Bunda Sari", "SPA-123", "Rp 150.000", "Bayar sekarang via QRIS"} {
		if !strings.Contains(body, want) {
			t.Errorf("booking_confirm missing %q:\n%s", want, body)
		}
	}
}

func TestRenderReceiptPaid(t *testing.T) {
	body, err := Render("payment_receipt", Payload{
		"code": "SPA-1", "amount": int64(75000), "method": "QRIS", "paid_at": "Senin · 14:05 WIB",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(body, "Lunas") || !strings.Contains(body, "75.000") {
		t.Errorf("receipt copy wrong:\n%s", body)
	}
}

func TestRenderTextFallback(t *testing.T) {
	body, err := Render("text", Payload{"text": "  Halo Bunda  "})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if body != "Halo Bunda" {
		t.Errorf("fallback = %q", body)
	}
	if _, err := Render("missing", Payload{}); err == nil {
		t.Fatal("expected an error for an unknown template")
	}
}
