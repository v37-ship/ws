package service

import (
	"testing"

	"spa-booking/internal/domain"
)

func TestSummarizeServiceHours(t *testing.T) {
	if got := SummarizeServiceHours(nil); got != "All day, every day" {
		t.Errorf("empty = %q", got)
	}
	mk := func(d int, from, to string) domain.ServiceHours {
		return domain.ServiceHours{Weekday: d, StartTime: from, EndTime: to}
	}
	hours := []domain.ServiceHours{
		mk(1, "15:00", "20:00"), mk(2, "15:00", "20:00"), mk(3, "15:00", "20:00"),
		mk(4, "15:00", "20:00"), mk(5, "15:00", "20:00"), mk(6, "09:00", "12:00"),
	}
	want := "Mon–Fri 15:00–20:00 · Sat 09:00–12:00 · closed other days"
	if got := SummarizeServiceHours(hours); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}
