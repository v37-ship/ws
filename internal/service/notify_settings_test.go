package service

import (
	"context"
	"testing"
	"time"
)

// TestReminderDisabledCancelsPending proves the reminder switch really stops
// messages: nothing new is scheduled and a queued row is dropped.
func TestReminderDisabledCancelsPending(t *testing.T) {
	notif := &fakeNotifier{}
	svc := newNotifyService(notif)
	svc.settings = newTestWaSettings(WaConfig{ReminderEnabled: false, CustomerEnabled: true, RemindHour: 18})

	b := guestBooking(fixedNow.Add(48 * time.Hour))
	svc.scheduleReminder(context.Background(), b, "Sari", b.GuestPhone)

	if notif.count("booking_remind") != 0 {
		t.Error("a disabled reminder must not be scheduled")
	}
	if len(notif.cancels) == 0 {
		t.Error("a disabled reminder must cancel any pending row")
	}
}

// TestReminderHourFromSettings proves the hour is read live, not from the
// startup snapshot.
func TestReminderHourFromSettings(t *testing.T) {
	notif := &fakeNotifier{}
	svc := newNotifyService(notif)
	svc.settings = newTestWaSettings(WaConfig{ReminderEnabled: true, CustomerEnabled: true, RemindHour: 9})

	b := guestBooking(fixedNow.Add(48 * time.Hour)) // Monday 26 Oct, 10:00 WIB
	svc.scheduleReminder(context.Background(), b, "Sari", b.GuestPhone)

	call, ok := notif.findCall("booking_remind")
	if !ok {
		t.Fatal("reminder not scheduled")
	}
	want := time.Date(2026, 10, 25, 9, 0, 0, 0, wib) // Sunday, live hour 09:00
	if !call.At.Equal(want) {
		t.Errorf("reminder at %s, want %s", call.At, want)
	}
}

// TestGroupModeFromSettings proves the group gate follows the live setting.
func TestGroupModeFromSettings(t *testing.T) {
	notif := &fakeNotifier{}
	svc := newNotifyService(notif)
	svc.settings = newTestWaSettings(WaConfig{GroupMode: "off", StaffGroupJID: "111@g.us"})

	b := guestBooking(fixedNow.Add(48 * time.Hour))
	svc.notifyGroupBooking(context.Background(), b, nil)
	svc.notifyGroupSlot(context.Background(), b, "cancelled", "customer", time.Time{})

	if len(notif.calls) != 0 {
		t.Errorf("group pings must be off, got %d calls", len(notif.calls))
	}
}

// TestCustomerDisabledFromSettings proves automatic customer messages follow
// the live switch.
func TestCustomerDisabledFromSettings(t *testing.T) {
	notif := &fakeNotifier{}
	svc := newNotifyService(notif)
	svc.settings = newTestWaSettings(WaConfig{CustomerEnabled: false})

	b := guestBooking(fixedNow.Add(48 * time.Hour))
	svc.notifyCustomerExpired(context.Background(), b)
	svc.notifyReviewRequest(context.Background(), b)

	if len(notif.calls) != 0 {
		t.Errorf("customer messages must be off, got %d calls", len(notif.calls))
	}
}
