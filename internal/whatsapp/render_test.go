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

func TestRenderReminder(t *testing.T) {
	body, err := Render("booking_remind", Payload{
		"name": "Bunda Sari", "service": "Baby Spa", "when": "Senin, 26 Oktober 2026 · 14:00 WIB",
		"staff": "Bidan Ayu", "location": "Kayu Putih", "amount": float64(150000),
		"pay_url": "https://wangga.biz.id/bookings/x/pay",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"Pengingat", "Baby Spa", "14:00 WIB", "Rp 150.000", "https://wangga.biz.id/bookings/x/pay"} {
		if !strings.Contains(body, want) {
			t.Errorf("booking_remind missing %q:\n%s", want, body)
		}
	}
}

func TestRenderLifecycle(t *testing.T) {
	body, err := Render("booking_cancelled", Payload{
		"name": "Bunda", "code": "SPA-1", "service": "Baby Spa", "when": "Senin · 14:00 WIB", "note": "sakit",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"dibatalkan", "SPA-1", "sakit", "pengembalian dana"} {
		if !strings.Contains(body, want) {
			t.Errorf("booking_cancelled missing %q:\n%s", want, body)
		}
	}

	body, err = Render("booking_rescheduled", Payload{
		"name": "Bunda", "code": "SPA-1", "service": "Baby Spa",
		"old_when": "Senin · 14:00 WIB", "when": "Selasa · 10:00 WIB", "staff": "Bidan Ayu",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"Sebelumnya: Senin", "Sekarang: *Selasa · 10:00 WIB*"} {
		if !strings.Contains(body, want) {
			t.Errorf("booking_rescheduled missing %q:\n%s", want, body)
		}
	}

	body, err = Render("booking_expired", Payload{
		"name": "Bunda", "code": "SPA-1", "service": "Baby Spa", "when": "Senin · 14:00 WIB",
		"book_url": "https://wangga.biz.id/book",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"hangus", "https://wangga.biz.id/book"} {
		if !strings.Contains(body, want) {
			t.Errorf("booking_expired missing %q:\n%s", want, body)
		}
	}
}

func TestRenderReviewRequest(t *testing.T) {
	body, err := Render("review_request", Payload{
		"name": "Bunda Sari", "service": "Baby Spa", "staff": "Bidan Ayu",
		"review_url": "https://g.page/r/x/review",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"Bunda Sari", "Bidan Ayu", "https://g.page/r/x/review"} {
		if !strings.Contains(body, want) {
			t.Errorf("review_request missing %q:\n%s", want, body)
		}
	}
}

func TestRenderStaffPings(t *testing.T) {
	body, err := Render("staff_booking", Payload{
		"when": "Senin · 14:00 WIB", "service": "Baby Spa", "staff": "Bidan Ayu",
		"name": "Sari", "phone": "628123", "address": "Jl. Melati 12", "band": "≤5 km",
		"amount": int64(155000), "paid": false, "source": "web",
		"portal_url": "https://wangga.biz.id/portal/bookings/1",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"Booking baru", "Sari", "628123", "Jl. Melati 12", "≤5 km", "BELUM BAYAR", "Rp 155.000"} {
		if !strings.Contains(body, want) {
			t.Errorf("staff_booking missing %q:\n%s", want, body)
		}
	}

	body, err = Render("staff_cancelled", Payload{
		"label": "BATAL", "when": "Senin · 14:00 WIB", "service": "Baby Spa", "staff": "Bidan Ayu",
		"name": "Sari", "actor": "customer", "portal_url": "https://wangga.biz.id/portal/bookings/1",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"BATAL", "oleh customer", "sudah kosong"} {
		if !strings.Contains(body, want) {
			t.Errorf("staff_cancelled missing %q:\n%s", want, body)
		}
	}

	body, err = Render("staff_rescheduled", Payload{
		"label": "JADWAL BERUBAH", "service": "Baby Spa", "staff": "Bidan Ayu", "name": "Sari",
		"old_when": "Senin · 14:00 WIB", "when": "Selasa · 10:00 WIB",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"Dari: Senin", "Ke: *Selasa · 10:00 WIB*"} {
		if !strings.Contains(body, want) {
			t.Errorf("staff_rescheduled missing %q:\n%s", want, body)
		}
	}
}

func TestRenderStaffDigest(t *testing.T) {
	items := []Payload{
		{"time": "09:00", "service": "Baby Spa", "staff": "Bidan Ayu", "who": "Sari", "place": "", "pay": "BELUM BAYAR"},
		{"time": "13:00", "service": "Mom Massage", "staff": "Bidan Rina", "who": "Dewi", "place": "🏠 ≤5 km", "pay": ""},
	}
	body, err := Render("staff_digest", Payload{
		"date": "Sabtu, 24 Oktober 2026", "items": items, "claims": 2, "tomorrow": 3,
		"portal_url": "https://wangga.biz.id/portal/bookings",
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"Sabtu, 24 Oktober 2026", "09:00", "Bidan Ayu", "BELUM BAYAR",
		"13:00", "🏠 ≤5 km", "Klaim bayar menunggu verifikasi: 2", "Besok: 3 booking",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("staff_digest missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Belum ada booking") {
		t.Errorf("staff_digest should not claim an empty day:\n%s", body)
	}

	empty, err := Render("staff_digest", Payload{"date": "Sabtu, 24 Oktober 2026", "items": []Payload{}, "tomorrow": 0})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(empty, "Belum ada booking hari ini") {
		t.Errorf("empty digest copy:\n%s", empty)
	}
}

func TestRenderOverrideSubstitutes(t *testing.T) {
	body, err := RenderOverride("booking_remind",
		"Halo {name}, {service} pada {when}. Total Rp {amount}.",
		Payload{"name": "Sari", "service": "Baby Spa", "when": "Senin · 14:00 WIB", "amount": float64(150000)})
	if err != nil {
		t.Fatalf("render override: %v", err)
	}
	want := "Halo Sari, Baby Spa pada Senin · 14:00 WIB. Total Rp 150.000."
	if body != want {
		t.Errorf("override = %q, want %q", body, want)
	}
}

func TestRenderOverrideRejects(t *testing.T) {
	if _, err := RenderOverride("booking_remind", "Halo {nmae}", Payload{"name": "Sari"}); err == nil {
		t.Error("unknown placeholder must fail")
	}
	if err := ValidateOverride("booking_remind", "   "); err == nil {
		t.Error("empty body must fail")
	}
	if err := ValidateOverride("booking_remind", strings.Repeat("a", 1001)); err == nil {
		t.Error("over-long body must fail")
	}
	if err := ValidateOverride("staff_digest", "halo"); err == nil {
		t.Error("staff templates are not editable")
	}
	if Editable("staff_digest") {
		t.Error("staff_digest must not be editable")
	}
}

// TestEditableTemplatesCoverFields guards the allowlist: every placeholder a
// template accepts must substitute with the sample payload, leaving no braces.
func TestEditableTemplatesCoverFields(t *testing.T) {
	for _, key := range EditableTemplates() {
		if !Editable(key) {
			t.Errorf("%s listed as editable but not registered", key)
		}
		if DefaultCopy(key) == "" {
			t.Errorf("%s has no default copy", key)
		}
		fields := Fields(key)
		if len(fields) == 0 {
			t.Errorf("%s has no placeholders", key)
		}
		var sb strings.Builder
		for _, f := range fields {
			sb.WriteString("{" + f + "} ")
		}
		out, err := RenderOverride(key, sb.String(), SamplePayload(key))
		if err != nil {
			t.Errorf("render every field of %s: %v", key, err)
			continue
		}
		if strings.ContainsAny(out, "{}") {
			t.Errorf("%s left a placeholder unsubstituted: %q", key, out)
		}
	}
}
