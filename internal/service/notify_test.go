package service

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
)

// fixedNow is the pinned clock used by the scheduling tests: Saturday
// 24 October 2026, 10:00 WIB.
var fixedNow = time.Date(2026, 10, 24, 10, 0, 0, 0, wib)

type waCall struct {
	Phone     string
	Template  string
	Payload   map[string]any
	BookingID *uuid.UUID
	Dedupe    string
	At        time.Time
	Upsert    bool
}

type cancelCall struct {
	BookingID uuid.UUID
	Keys      []string
}

type fakeNotifier struct {
	calls   []waCall
	cancels []cancelCall
}

func (f *fakeNotifier) Enqueue(_ context.Context, phone, key string, payload map[string]any, id *uuid.UUID, dedupe string, at time.Time) (bool, error) {
	f.calls = append(f.calls, waCall{phone, key, payload, id, dedupe, at, false})
	return true, nil
}

func (f *fakeNotifier) UpsertScheduled(_ context.Context, phone, key string, payload map[string]any, id *uuid.UUID, dedupe string, at time.Time) (bool, error) {
	f.calls = append(f.calls, waCall{phone, key, payload, id, dedupe, at, true})
	return true, nil
}

func (f *fakeNotifier) CancelPending(_ context.Context, id uuid.UUID, keys ...string) (int64, error) {
	f.cancels = append(f.cancels, cancelCall{id, keys})
	return int64(len(keys)), nil
}

// findCall returns the first call with a template key, or zero when absent.
func (f *fakeNotifier) findCall(templateKey string) (waCall, bool) {
	for _, c := range f.calls {
		if c.Template == templateKey {
			return c, true
		}
	}
	return waCall{}, false
}

func (f *fakeNotifier) count(templateKey string) int {
	n := 0
	for _, c := range f.calls {
		if c.Template == templateKey {
			n++
		}
	}
	return n
}

// newNotifyService builds a BookingService wired only for messaging: guest
// bookings mean contact() never reaches a repository.
func newNotifyService(wa Notifier, mutate ...func(*BookingOptions)) *BookingService {
	opts := BookingOptions{
		PaymentMode:  "qris_manual",
		Wa:           wa,
		CustomerWA:   true,
		StaffGroup:   "6281234567890-123@g.us",
		GroupBooking: "web",
		RemindHour:   18,
		ReviewURL:    "https://g.page/r/CTLPrFudX25eEBM/review",
	}
	for _, fn := range mutate {
		fn(&opts)
	}
	return &BookingService{
		opts:    opts,
		baseURL: "https://wangga.biz.id",
		log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		now:     func() time.Time { return fixedNow },
	}
}

// guestBooking is a confirmed guest visit at the given WIB time.
func guestBooking(start time.Time) *domain.Booking {
	token := "tok"
	return &domain.Booking{
		ID:          uuid.New(),
		GuestName:   "Sari Melati",
		GuestPhone:  "628111222333",
		AccessToken: &token,
		Status:      domain.BookingConfirmed,
		StartAt:     start,
		ServiceName: "Baby Spa",
		StaffName:   "Bidan Ayu",
		Source:      "web",
		PriceIDR:    150000,
	}
}

func wibAt(hour, min int) time.Time {
	return time.Date(2026, 10, 24, hour, min, 0, 0, wib)
}

func TestReminderScheduledEveningBefore(t *testing.T) {
	wa := &fakeNotifier{}
	svc := newNotifyService(wa)
	// Tomorrow 11:00 WIB: the day-before 18:00 has not passed yet.
	b := guestBooking(wibAt(11, 0).AddDate(0, 0, 1))
	svc.scheduleReminder(context.Background(), b, "Sari Melati", b.GuestPhone)

	call, ok := wa.findCall("booking_remind")
	if !ok {
		t.Fatalf("no reminder queued, cancels=%v", wa.cancels)
	}
	if !call.Upsert {
		t.Error("reminder must be queued with UpsertScheduled so a reschedule can move it")
	}
	if call.Phone != b.GuestPhone {
		t.Errorf("phone = %q, want the guest number", call.Phone)
	}
	if want := wibAt(18, 0); !call.At.Equal(want) {
		t.Errorf("scheduled at %s, want %s", call.At, want)
	}
	if call.Dedupe != "remind:"+b.ID.String() {
		t.Errorf("dedupe = %q, want a stable per-booking key", call.Dedupe)
	}
	if call.Payload["service"] != "Baby Spa" || call.Payload["name"] != "Sari Melati" {
		t.Errorf("payload = %v", call.Payload)
	}
}

func TestReminderFallsBackToMorning(t *testing.T) {
	wa := &fakeNotifier{}
	svc := newNotifyService(wa)
	// Booked at 19:00 for 20:00 tomorrow: the day-before slot is gone, so the
	// evening booking still gets a morning reminder.
	svc.now = func() time.Time { return wibAt(19, 0) }
	b := guestBooking(wibAt(20, 0).AddDate(0, 0, 1))
	svc.scheduleReminder(context.Background(), b, "Sari", b.GuestPhone)

	call, ok := wa.findCall("booking_remind")
	if !ok {
		t.Fatalf("no reminder queued (cancels=%v)", wa.cancels)
	}
	if want := wibAt(19, 0).Add(remindMinLead); !call.At.Equal(want) {
		t.Errorf("scheduled at %s, want %s", call.At, want)
	}
}

func TestReminderSkipsCloseBookings(t *testing.T) {
	wa := &fakeNotifier{}
	svc := newNotifyService(wa)
	// 11 hours out: the confirmation already covers it.
	b := guestBooking(wibAt(21, 0))
	svc.scheduleReminder(context.Background(), b, "Sari", b.GuestPhone)

	if len(wa.calls) != 0 {
		t.Errorf("queued %d messages for a booking 11h away: %v", len(wa.calls), wa.calls)
	}
	if len(wa.cancels) != 1 || len(wa.cancels[0].Keys) != 1 {
		t.Errorf("expected the pending reminder to be cancelled, got %v", wa.cancels)
	}
}

func TestReminderSkipsWhenMorningSendIsTooLate(t *testing.T) {
	wa := &fakeNotifier{}
	// Pin the clock at 20:00 so tomorrow 09:00 is 13h out but the fallback
	// (07:00) would leave less than two hours before the visit.
	svc := newNotifyService(wa)
	svc.now = func() time.Time { return time.Date(2026, 10, 24, 20, 0, 0, 0, wib) }
	b := guestBooking(wibAt(9, 0).AddDate(0, 0, 1))
	svc.scheduleReminder(context.Background(), b, "Sari", b.GuestPhone)

	if len(wa.calls) != 0 {
		t.Errorf("queued a reminder with no usable window: %v", wa.calls)
	}
	if len(wa.cancels) != 1 {
		t.Errorf("expected a cancel, got %v", wa.cancels)
	}
}

func TestReminderMovesOnReschedule(t *testing.T) {
	wa := &fakeNotifier{}
	svc := newNotifyService(wa)
	b := guestBooking(wibAt(11, 0).AddDate(0, 0, 1))
	svc.scheduleReminder(context.Background(), b, "Sari", b.GuestPhone)

	b.StartAt = wibAt(15, 0).AddDate(0, 0, 1)
	svc.scheduleReminder(context.Background(), b, "Sari", b.GuestPhone)

	if n := wa.count("booking_remind"); n != 2 {
		t.Fatalf("expected two upserts for one booking, got %d", n)
	}
	first, second := wa.calls[0], wa.calls[1]
	if first.Dedupe != second.Dedupe {
		t.Errorf("dedupe keys differ across reschedules: %q vs %q", first.Dedupe, second.Dedupe)
	}
	if !second.At.Equal(wibAt(18, 0)) {
		t.Errorf("second send at %s, want the same evening-before slot", second.At)
	}
	if second.Payload["when"] != formatWIB(b.StartAt) {
		t.Errorf("payload was not refreshed: %v", second.Payload["when"])
	}
}

func TestReviewRequestScheduledAfterVisit(t *testing.T) {
	wa := &fakeNotifier{}
	svc := newNotifyService(wa)
	b := guestBooking(wibAt(15, 0))
	b.Status = domain.BookingCompleted
	svc.notifyReviewRequest(context.Background(), b)

	call, ok := wa.findCall("review_request")
	if !ok {
		t.Fatalf("no review request queued (cancels=%v)", wa.cancels)
	}
	if !call.Upsert || call.Dedupe != "review:"+b.ID.String() {
		t.Errorf("call = %+v", call)
	}
	if want := fixedNow.Add(2 * time.Hour); !call.At.Equal(want) {
		t.Errorf("scheduled at %s, want %s", call.At, want)
	}
	if call.Payload["review_url"] != svc.opts.ReviewURL {
		t.Errorf("review_url = %v", call.Payload["review_url"])
	}
}

func TestReviewRequestClampedToMorning(t *testing.T) {
	wa := &fakeNotifier{}
	svc := newNotifyService(wa)
	svc.now = func() time.Time { return time.Date(2026, 10, 24, 19, 30, 0, 0, wib) }
	b := guestBooking(wibAt(19, 0))
	b.Status = domain.BookingCompleted
	svc.notifyReviewRequest(context.Background(), b)

	call, _ := wa.findCall("review_request")
	if want := time.Date(2026, 10, 25, sendWindowStart, 0, 0, 0, wib); !call.At.Equal(want) {
		t.Errorf("scheduled at %s, want the next 07:00 WIB", call.At)
	}
}

func TestGroupPingOnlyForOnlineBookings(t *testing.T) {
	wa := &fakeNotifier{}
	svc := newNotifyService(wa)

	web := guestBooking(wibAt(11, 0).AddDate(0, 0, 1))
	svc.notifyGroupBooking(context.Background(), web, nil)
	if n := wa.count("staff_booking"); n != 1 {
		t.Fatalf("online booking pinged %d times, want 1", n)
	}
	call, _ := wa.findCall("staff_booking")
	if call.Phone != svc.opts.StaffGroup {
		t.Errorf("ping went to %q, want the staff group", call.Phone)
	}
	if call.Dedupe != "booking:"+web.ID.String()+":staff" {
		t.Errorf("dedupe = %q", call.Dedupe)
	}
	if call.Payload["phone"] != web.GuestPhone || call.Payload["source"] != "web" {
		t.Errorf("payload = %v", call.Payload)
	}

	walkin := guestBooking(wibAt(11, 0).AddDate(0, 0, 1))
	walkin.Source = "walkin"
	svc.notifyGroupBooking(context.Background(), walkin, nil)
	if n := wa.count("staff_booking"); n != 1 {
		t.Errorf("walk-in must not ping the group (count=%d)", n)
	}
}

func TestGroupPingModes(t *testing.T) {
	wa := &fakeNotifier{}
	svc := newNotifyService(wa, func(o *BookingOptions) { o.GroupBooking = "all" })
	walkin := guestBooking(wibAt(11, 0).AddDate(0, 0, 1))
	walkin.Source = "walkin"
	svc.notifyGroupBooking(context.Background(), walkin, nil)
	if n := wa.count("staff_booking"); n != 1 {
		t.Errorf("mode=all pinged %d times, want 1", n)
	}

	off := newNotifyService(wa, func(o *BookingOptions) { o.GroupBooking = "off" })
	off.notifyGroupSlot(context.Background(), walkin, "cancelled", "customer", time.Time{})
	if n := wa.count("staff_cancelled"); n != 0 {
		t.Errorf("mode=off still pinged %d times", n)
	}

	// Without a group JID there is nowhere to send.
	wa2 := &fakeNotifier{}
	noGroup := newNotifyService(wa2, func(o *BookingOptions) { o.StaffGroup = "" })
	noGroup.notifyGroupSlot(context.Background(), walkin, "cancelled", "customer", time.Time{})
	if len(wa2.calls) != 0 {
		t.Errorf("pinged without a group JID: %v", wa2.calls)
	}
}

func TestGroupSlotPings(t *testing.T) {
	wa := &fakeNotifier{}
	svc := newNotifyService(wa)
	b := guestBooking(wibAt(11, 0).AddDate(0, 0, 1))
	old := wibAt(9, 0).AddDate(0, 0, 1)

	svc.notifyGroupSlot(context.Background(), b, "cancelled", "customer", time.Time{})
	svc.notifyGroupSlot(context.Background(), b, "expired", "sistem", time.Time{})
	svc.notifyGroupSlot(context.Background(), b, "rescheduled", "staf", old)

	if n := wa.count("staff_cancelled"); n != 2 {
		t.Errorf("cancel/expire pings = %d, want 2", n)
	}
	cancelled, _ := wa.findCall("staff_cancelled")
	if cancelled.Payload["label"] != "BATAL" || cancelled.Payload["actor"] != "customer" {
		t.Errorf("cancel payload = %v", cancelled.Payload)
	}
	resched, ok := wa.findCall("staff_rescheduled")
	if !ok {
		t.Fatal("reschedule ping missing")
	}
	if resched.Payload["old_when"] != formatWIB(old) {
		t.Errorf("old_when = %v, want %v", resched.Payload["old_when"], formatWIB(old))
	}
}

func TestCustomerLifecycleMessages(t *testing.T) {
	wa := &fakeNotifier{}
	svc := newNotifyService(wa)
	b := guestBooking(wibAt(11, 0).AddDate(0, 0, 1))

	svc.notifyCustomerCancelled(context.Background(), b, "tidak bisa hadir")
	cancelled, ok := wa.findCall("booking_cancelled")
	if !ok {
		t.Fatal("cancellation message missing")
	}
	if cancelled.Phone != b.GuestPhone || cancelled.Payload["note"] != "tidak bisa hadir" {
		t.Errorf("call = %+v", cancelled)
	}
	// The customer sees the same reference as on the pay page.
	if want := orderRef(b.ID); cancelled.Payload["code"] != want {
		t.Errorf("code = %v, want %v", cancelled.Payload["code"], want)
	}

	svc.notifyCustomerExpired(context.Background(), b)
	if _, ok := wa.findCall("booking_expired"); !ok {
		t.Error("expiry message missing")
	}

	old := wibAt(9, 0).AddDate(0, 0, 1)
	svc.notifyCustomerRescheduled(context.Background(), b, old)
	resched, ok := wa.findCall("booking_rescheduled")
	if !ok {
		t.Fatal("reschedule message missing")
	}
	if resched.Payload["old_when"] != formatWIB(old) || resched.Payload["when"] != formatWIB(b.StartAt) {
		t.Errorf("reschedule payload = %v", resched.Payload)
	}
}

func TestCustomerSwitches(t *testing.T) {
	wa := &fakeNotifier{}
	svc := newNotifyService(wa, func(o *BookingOptions) { o.CustomerWA = false })
	b := guestBooking(wibAt(11, 0).AddDate(0, 0, 1))

	svc.onConfirmed(context.Background(), b, nil, "Sari", b.GuestPhone)
	if wa.count("booking_confirm") != 0 || wa.count("booking_remind") != 0 {
		t.Errorf("customer messages must respect WA_CUSTOMER_ENABLED: %v", wa.calls)
	}

	// The staff group is independent of the customer switch.
	if n := wa.count("staff_booking"); n != 1 {
		t.Errorf("staff ping = %d, want 1", n)
	}

	// Without a notifier at all nothing is queued and nothing panics.
	bare := newNotifyService(nil)
	bare.scheduleReminder(context.Background(), b, "Sari", b.GuestPhone)
	bare.notifyCustomerExpired(context.Background(), b)
	bare.notifyGroupSlot(context.Background(), b, "cancelled", "sistem", time.Time{})
}

func TestOnConfirmedQueuesConfirmReminderAndPing(t *testing.T) {
	wa := &fakeNotifier{}
	svc := newNotifyService(wa)
	b := guestBooking(wibAt(11, 0).AddDate(0, 0, 1))

	svc.onConfirmed(context.Background(), b, nil, "Sari Melati", b.GuestPhone)
	for _, key := range []string{"booking_confirm", "booking_remind", "staff_booking"} {
		if wa.count(key) != 1 {
			t.Errorf("%s queued %d times, want 1", key, wa.count(key))
		}
	}

	// A pending booking must not be announced as confirmed.
	pending := guestBooking(wibAt(11, 0).AddDate(0, 0, 1))
	pending.Status = domain.BookingPendingPayment
	before := len(wa.calls)
	svc.onConfirmed(context.Background(), pending, nil, "Sari", pending.GuestPhone)
	if len(wa.calls) != before {
		t.Errorf("pending booking queued %d messages", len(wa.calls)-before)
	}
}

func TestOrderRefMatchesPayCode(t *testing.T) {
	// Mirrors the order id the pay page prints (SPA-<16 hex>).
	id := uuid.MustParse("2210c24f-2b24-4a7c-8dcd-2d8a0e3355c3")
	if got := orderRef(id); got != "SPA-2210c24f2b244a7c" {
		t.Errorf("orderRef = %q", got)
	}
}

func TestClampSendTime(t *testing.T) {
	cases := []struct {
		in   time.Time
		want time.Time
	}{
		{wibAt(5, 30), wibAt(7, 0)},
		{wibAt(12, 0), wibAt(12, 0)},
		{wibAt(21, 15), time.Date(2026, 10, 25, 7, 0, 0, 0, wib)},
	}
	for _, tc := range cases {
		if got := clampSendTime(tc.in); !got.Equal(tc.want) {
			t.Errorf("clampSendTime(%s) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestBandLabel(t *testing.T) {
	km := 5.0
	if got := bandLabel(&domain.Booking{DistanceKm: &km}); got != "≤5 km" {
		t.Errorf("bandLabel = %q", got)
	}
	if got := bandLabel(&domain.Booking{}); got != "" {
		t.Errorf("bandLabel without a band = %q", got)
	}
}
