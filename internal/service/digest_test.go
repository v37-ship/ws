package service

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/whatsapp"

	"github.com/google/uuid"
)

// testLogger discards log output in tests.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestDigestKeyOnceADay(t *testing.T) {
	day := digestKey(fixedNow, false)
	if day != "digest:2026-10-24" {
		t.Errorf("daily key = %q", day)
	}
	if again := digestKey(fixedNow.Add(3*time.Hour), false); again != day {
		t.Errorf("key changed within the day: %q vs %q", again, day)
	}
	manual := digestKey(fixedNow, true)
	if !strings.HasPrefix(manual, "digest:manual:2026-10-24:") {
		t.Errorf("manual key = %q", manual)
	}
	if manual == digestKey(fixedNow, true) {
		t.Error("manual keys must differ so a staff retry is not swallowed")
	}
}

func TestDigestDue(t *testing.T) {
	ctx := context.Background()
	svc := NewDigestService(nil, &fakeNotifier{}, nil, "g@g.us", "https://x.test", 7, testLogger())
	if svc.Due(ctx, wibAt(6, 59)) {
		t.Error("digest is due before its hour")
	}
	if !svc.Due(ctx, wibAt(7, 0)) {
		t.Error("digest is not due at its hour")
	}
	if !svc.Due(ctx, wibAt(15, 30)) {
		t.Error("a late start must still get the agenda out")
	}
	if NewDigestService(nil, nil, nil, "", "", 7, testLogger()).Configured(ctx) {
		t.Error("no group JID means not configured")
	}
	if !svc.Configured(ctx) {
		t.Error("configured service reported as unconfigured")
	}
}

// TestDigestLiveSettings proves the on/off and hour come from the settings
// service, not the constructor.
func TestDigestLiveSettings(t *testing.T) {
	ctx := context.Background()
	svc := NewDigestService(nil, &fakeNotifier{}, nil, "g@g.us", "https://x.test", 7, testLogger())
	svc.settings = newTestWaSettings(WaConfig{DigestEnabled: false, DigestHour: 7, StaffGroupJID: "g@g.us"})
	if svc.Due(ctx, wibAt(9, 0)) {
		t.Error("a disabled digest is never due")
	}
	svc.settings = newTestWaSettings(WaConfig{DigestEnabled: true, DigestHour: 10, StaffGroupJID: "g@g.us"})
	if svc.Due(ctx, wibAt(9, 0)) {
		t.Error("digest before the live hour")
	}
	if !svc.Due(ctx, wibAt(10, 0)) {
		t.Error("digest at the live hour")
	}
	if !svc.Configured(ctx) {
		t.Error("a group from settings should count as configured")
	}
}

func TestDigestPayloadAndCopy(t *testing.T) {
	km := 5.0
	cid := uuid.New()
	today := []domain.Booking{
		{ID: uuid.New(), CustomerID: &cid, Status: domain.BookingConfirmed, StartAt: wibAt(9, 0), ServiceName: "Baby Spa",
			StaffName: "Bidan Ayu", CustomerName: "Sari Melati", PaymentStatus: domain.PaymentPending},
		{ID: uuid.New(), Status: domain.BookingCancelled, StartAt: wibAt(10, 0), ServiceName: "Cancelled One"},
		{ID: uuid.New(), Status: domain.BookingCheckedIn, StartAt: wibAt(13, 0), ServiceName: "Mom Massage",
			StaffName: "Bidan Rina", GuestName: "Dewi Ayu", Place: domain.PlaceHomecare,
			DistanceKm: &km, PaymentStatus: domain.PaymentPaid},
	}
	tomorrow := []domain.Booking{
		{Status: domain.BookingConfirmed, StartAt: wibAt(9, 0).AddDate(0, 0, 1)},
		{Status: domain.BookingNoShow, StartAt: wibAt(11, 0).AddDate(0, 0, 1)},
	}

	payload := digestPayload(today, tomorrow, 2, fixedNow)
	if payload["date"] != "Sabtu, 24 Oktober 2026" {
		t.Errorf("date = %v", payload["date"])
	}
	if got := payload["tomorrow"]; got != 1 {
		t.Errorf("tomorrow = %v, want only the live booking", got)
	}
	items, _ := payload["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %d, want cancelled bookings dropped", len(items))
	}
	first, _ := items[0].(map[string]any)
	if first["who"] != "Sari" || first["pay"] != "BELUM BAYAR" || first["place"] != "" {
		t.Errorf("first item = %v", first)
	}
	second, _ := items[1].(map[string]any)
	if second["who"] != "Dewi" || second["pay"] != "" || second["place"] == "" {
		t.Errorf("second item = %v", second)
	}

	body, err := whatsapp.Render("staff_digest", whatsapp.Payload(payload))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		"Sabtu, 24 Oktober 2026", "09:00", "Bidan Ayu", "BELUM BAYAR",
		"13:00", "🏠 ≤5 km", "Klaim bayar menunggu verifikasi: 2", "Besok: 1 booking",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("digest missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "Cancelled One") {
		t.Errorf("digest listed a cancelled booking:\n%s", body)
	}
}
