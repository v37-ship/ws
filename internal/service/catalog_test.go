package service

import (
	"testing"
	"time"

	"spa-booking/internal/domain"
)

func TestSummarizeServiceHours(t *testing.T) {
	if got := SummarizeServiceHours(nil, "en"); got != "All day, every day" {
		t.Errorf("empty en = %q", got)
	}
	if got := SummarizeServiceHours(nil, "id"); got != "Buka setiap hari" {
		t.Errorf("empty id = %q", got)
	}
	mk := func(d int, from, to string) domain.ServiceHours {
		return domain.ServiceHours{Weekday: d, StartTime: from, EndTime: to}
	}
	hours := []domain.ServiceHours{
		mk(1, "15:00", "20:00"), mk(2, "15:00", "20:00"), mk(3, "15:00", "20:00"),
		mk(4, "15:00", "20:00"), mk(5, "15:00", "20:00"), mk(6, "09:00", "12:00"),
	}
	want := "Mon–Fri 15:00–20:00 · Sat 09:00–12:00 · closed other days"
	if got := SummarizeServiceHours(hours, "en"); got != want {
		t.Errorf("got %q want %q", got, want)
	}
	wantID := "Sen–Jum 15:00–20:00 · Sab 09:00–12:00 · hari lain tutup"
	if got := SummarizeServiceHours(hours, "id"); got != wantID {
		t.Errorf("got %q want %q", got, wantID)
	}
}

func TestDedupeSlots(t *testing.T) {
	mk := func(min int, avail bool) SlotInfo {
		return SlotInfo{
			Slot:      domain.Slot{StartAt: time.Date(2026, 10, 5, 15, min, 0, 0, time.UTC)},
			Available: avail,
		}
	}
	in := []SlotInfo{
		mk(0, false),
		mk(0, true),
		mk(15, true),
		mk(15, true),
		mk(30, false),
		mk(30, false),
	}
	out := dedupeSlots(in)
	if len(out) != 3 {
		t.Fatalf("got %d slots want 3", len(out))
	}
	if !out[0].Available {
		t.Errorf("first hour should prefer the free slot")
	}
	if out[1].Slot.StartAt.Minute() != 15 || !out[1].Available {
		t.Errorf("second hour wrong: %+v", out[1])
	}
	if out[2].Available {
		t.Errorf("all-busy hour should stay taken")
	}
	if got := len(dedupeSlots(nil)); got != 0 {
		t.Errorf("nil = %d want 0", got)
	}
}
