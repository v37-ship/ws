package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
)

// Notifier is the outbox surface used for transactional WhatsApp messages.
// WaService implements it; tests can fake it.
type Notifier interface {
	Enqueue(ctx context.Context, phone, templateKey string, payload map[string]any, bookingID *uuid.UUID, dedupeKey string, scheduledAt time.Time) (bool, error)
	UpsertScheduled(ctx context.Context, phone, templateKey string, payload map[string]any, bookingID *uuid.UUID, dedupeKey string, scheduledAt time.Time) (bool, error)
	CancelPending(ctx context.Context, bookingID uuid.UUID, keys ...string) (int64, error)
}

// BookingOptions carries the deployment switches for payment and messaging.
type BookingOptions struct {
	PaymentMode  string // "qris_manual" or "midtrans"
	Wa           Notifier
	CustomerWA   bool   // queue automatic customer messages
	StaffGroup   string // Evolution group JID for staff alerts
	GroupBooking string // group booking pings: "off", "web" (default) or "all"
	RemindHour   int    // WIB hour of the H-1 reminder (default 18)
	ReviewURL    string // Google review link for the post-visit request
}

// wib is the spa's operating timezone. Every scheduling decision (reminders,
// review requests, the daily digest) is made in this zone.
var wib = time.FixedZone("WIB", 7*3600)

// nowWIB is the current time in the spa's timezone (tests can pin it).
func (s *BookingService) nowWIB() time.Time {
	if s.now != nil {
		return s.now().In(wib)
	}
	return time.Now().In(wib)
}

const (
	sendWindowStart = 7  // automatic customer messages never go out before 07:00 WIB
	sendWindowEnd   = 20 // ... or after 20:00 WIB
	remindMinLead   = 12 * time.Hour
)

// clampSendTime moves a send into the 07:00-20:00 WIB window so automatic
// customer messages never wake anyone up.
func clampSendTime(at time.Time) time.Time {
	t := at.In(wib)
	switch {
	case t.Hour() < sendWindowStart:
		return time.Date(t.Year(), t.Month(), t.Day(), sendWindowStart, 0, 0, 0, wib)
	case t.Hour() >= sendWindowEnd:
		return time.Date(t.Year(), t.Month(), t.Day()+1, sendWindowStart, 0, 0, 0, wib)
	}
	return t
}

// remindAt returns when the H-1 reminder should go out for a booking, or the
// zero time when there is no useful window left (the confirmation message
// already covered it, or the visit is too close).
func (s *BookingService) remindAt(b *domain.Booking) time.Time {
	now := s.nowWIB()
	visit := b.StartAt.In(wib)
	if visit.Sub(now) < remindMinLead {
		return time.Time{}
	}
	hour := s.opts.RemindHour
	if hour <= 0 || hour > 23 {
		hour = 18
	}
	at := time.Date(visit.Year(), visit.Month(), visit.Day(), hour, 0, 0, 0, wib).AddDate(0, 0, -1)
	if !at.After(now.Add(30 * time.Minute)) {
		// Booked after the day-before cutoff (or for tomorrow evening): send
		// it in the morning instead of skipping it.
		at = now.Add(remindMinLead)
	}
	at = clampSendTime(at)
	if !at.Add(2 * time.Hour).Before(visit) {
		return time.Time{}
	}
	return at
}

// onConfirmed runs everything that follows a confirmed booking: the customer
// confirmation, the scheduled H-1 reminder, and the staff group ping. All
// three are idempotent, so a repeated call cannot double-send.
func (s *BookingService) onConfirmed(ctx context.Context, b *domain.Booking, p *domain.Payment, name, phone string) {
	if b.Status != domain.BookingConfirmed {
		return
	}
	s.notifyBooking(ctx, b, p, name, phone)
	s.scheduleReminder(ctx, b, name, phone)
	s.notifyGroupBooking(ctx, b, p)
}

// ManualPayments reports whether bookings are confirmed without a gateway.
func (s *BookingService) ManualPayments() bool { return s.opts.PaymentMode == "qris_manual" }

func (s *BookingService) payURL(b *domain.Booking) string {
	if b.IsGuest() && b.AccessToken != nil {
		return fmt.Sprintf("%s/bookings/%s/pay?t=%s", s.baseURL, b.ID.String(), *b.AccessToken)
	}
	return fmt.Sprintf("%s/bookings/%s/pay", s.baseURL, b.ID.String())
}

// notifyBooking queues the booking confirmation for the customer. It is a
// no-op in midtrans mode (the payment webhook sends the confirmation) or when
// customer messaging is disabled.
func (s *BookingService) notifyBooking(ctx context.Context, b *domain.Booking, p *domain.Payment, name, phone string) {
	if s.opts.Wa == nil || !s.opts.CustomerWA || !s.ManualPayments() {
		return
	}
	payload := s.waPayload(b, p)
	payload["name"] = displayName(name)
	if _, err := s.opts.Wa.Enqueue(ctx, phone, "booking_confirm", payload, &b.ID,
		"booking:"+b.ID.String()+":confirm", time.Time{}); err != nil {
		s.log.Warn("wa enqueue booking confirm", "booking", b.ID, "err", err)
	}
}

// notifyReceipt queues the payment receipt after a manual confirmation.
func (s *BookingService) notifyReceipt(ctx context.Context, b *domain.Booking, p *domain.Payment, name, phone string) {
	if s.opts.Wa == nil || !s.opts.CustomerWA {
		return
	}
	payload := s.waPayload(b, p)
	payload["name"] = displayName(name)
	if _, err := s.opts.Wa.Enqueue(ctx, phone, "payment_receipt", payload, &b.ID,
		"booking:"+b.ID.String()+":receipt", time.Time{}); err != nil {
		s.log.Warn("wa enqueue receipt", "booking", b.ID, "err", err)
	}
}

// notifyStaffClaim alerts the staff group that a customer reported a payment.
func (s *BookingService) notifyStaffClaim(ctx context.Context, b *domain.Booking, p *domain.Payment) {
	if s.opts.Wa == nil || strings.TrimSpace(s.opts.StaffGroup) == "" {
		return
	}
	payload := s.waPayload(b, p)
	dedupe := fmt.Sprintf("claim:%s:%d", b.ID.String(), time.Now().UnixNano())
	if _, err := s.opts.Wa.Enqueue(ctx, s.opts.StaffGroup, "staff_paid_claim", payload, &b.ID,
		dedupe, time.Time{}); err != nil {
		s.log.Warn("wa enqueue staff claim", "booking", b.ID, "err", err)
	}
}

// notifyClaimRejected tells the customer their proof was not accepted yet.
func (s *BookingService) notifyClaimRejected(ctx context.Context, b *domain.Booking, phone string) {
	if s.opts.Wa == nil || !s.opts.CustomerWA {
		return
	}
	payload := s.waPayload(b, nil)
	if p, err := s.payments.ByBookingID(ctx, b.ID); err == nil {
		payload = s.waPayload(b, p)
	}
	dedupe := fmt.Sprintf("reject:%s:%d", b.ID.String(), time.Now().UnixNano())
	if _, err := s.opts.Wa.Enqueue(ctx, phone, "claim_rejected", payload, &b.ID,
		dedupe, time.Time{}); err != nil {
		s.log.Warn("wa enqueue claim rejected", "booking", b.ID, "err", err)
	}
}

// ---- scheduled messages: H-1 reminder + review request ----

// remindKey / reviewKey are stable dedupe keys. Because the outbox key is
// unique forever, a reschedule must move the existing row (UpsertScheduled)
// rather than insert a second one.
func remindKey(id uuid.UUID) string { return "remind:" + id.String() }
func reviewKey(id uuid.UUID) string { return "review:" + id.String() }

// scheduleReminder queues (or moves) the H-1 reminder for a booking, and drops
// any pending one when there is no useful window left.
func (s *BookingService) scheduleReminder(ctx context.Context, b *domain.Booking, name, phone string) {
	if s.opts.Wa == nil {
		return
	}
	at := s.remindAt(b)
	if at.IsZero() {
		s.cancelReminder(ctx, b.ID)
		return
	}
	if !s.opts.CustomerWA {
		return
	}
	payload := s.waPayload(b, nil)
	payload["name"] = displayName(name)
	if _, err := s.opts.Wa.UpsertScheduled(ctx, phone, "booking_remind", payload, &b.ID,
		remindKey(b.ID), at); err != nil {
		s.log.Warn("wa schedule reminder", "booking", b.ID, "err", err)
	}
}

// cancelReminder drops a reminder that has not gone out yet. It runs even when
// customer messaging is switched off, so a row queued earlier cannot fire
// after the booking is cancelled or moved too close to the visit.
func (s *BookingService) cancelReminder(ctx context.Context, id uuid.UUID) {
	if s.opts.Wa == nil {
		return
	}
	if _, err := s.opts.Wa.CancelPending(ctx, id, remindKey(id)); err != nil {
		s.log.Warn("wa cancel reminder", "booking", id, "err", err)
	}
}

// notifyReviewRequest queues the post-visit review ask (two hours after the
// visit, clamped to waking hours).
func (s *BookingService) notifyReviewRequest(ctx context.Context, b *domain.Booking) {
	if s.opts.Wa == nil || !s.opts.CustomerWA || strings.TrimSpace(s.opts.ReviewURL) == "" {
		return
	}
	name, phone := s.contact(ctx, b)
	if strings.TrimSpace(phone) == "" {
		return
	}
	payload := map[string]any{
		"name":       displayName(name),
		"service":    b.ServiceName,
		"staff":      b.StaffName,
		"review_url": s.opts.ReviewURL,
	}
	at := clampSendTime(s.nowWIB().Add(2 * time.Hour))
	if _, err := s.opts.Wa.UpsertScheduled(ctx, phone, "review_request", payload, &b.ID,
		reviewKey(b.ID), at); err != nil {
		s.log.Warn("wa schedule review request", "booking", b.ID, "err", err)
	}
}

// ---- customer lifecycle messages ----

// notifyCustomer queues a lifecycle message (cancel/reschedule/expired) to the
// booking's WhatsApp number, account or guest.
func (s *BookingService) notifyCustomer(ctx context.Context, b *domain.Booking, key string, extra map[string]any) {
	if s.opts.Wa == nil || !s.opts.CustomerWA {
		return
	}
	name, phone := s.contact(ctx, b)
	if strings.TrimSpace(phone) == "" {
		return
	}
	payload := s.waPayload(b, nil)
	payload["name"] = displayName(name)
	for k, v := range extra {
		payload[k] = v
	}
	dedupe := fmt.Sprintf("%s:%s:%d", key, b.ID.String(), time.Now().UnixNano())
	if _, err := s.opts.Wa.Enqueue(ctx, phone, key, payload, &b.ID, dedupe, time.Time{}); err != nil {
		s.log.Warn("wa enqueue "+key, "booking", b.ID, "err", err)
	}
}

func (s *BookingService) notifyCustomerCancelled(ctx context.Context, b *domain.Booking, note string) {
	s.notifyCustomer(ctx, b, "booking_cancelled", map[string]any{"note": strings.TrimSpace(note)})
}

func (s *BookingService) notifyCustomerExpired(ctx context.Context, b *domain.Booking) {
	s.notifyCustomer(ctx, b, "booking_expired", nil)
}

func (s *BookingService) notifyCustomerRescheduled(ctx context.Context, b *domain.Booking, oldStart time.Time) {
	s.notifyCustomer(ctx, b, "booking_rescheduled", map[string]any{"old_when": formatWIB(oldStart)})
}

// ---- staff group pings ----

// groupEnabled reports whether booking pings have both a destination and are
// switched on at all.
func (s *BookingService) groupEnabled() bool {
	if s.opts.Wa == nil || strings.TrimSpace(s.opts.StaffGroup) == "" {
		return false
	}
	return s.opts.GroupBooking != "off"
}

// groupBookingPing decides whether a *new booking* pings the group: online
// bookings by default, every source with "all". A booking typed in by CS never
// pings — the staff member creating it is the one reading the group.
func (s *BookingService) groupBookingPing(source string) bool {
	if !s.groupEnabled() {
		return false
	}
	return s.opts.GroupBooking == "all" || source == "web"
}

// notifyGroupBooking tells the staff group that a new booking landed.
func (s *BookingService) notifyGroupBooking(ctx context.Context, b *domain.Booking, p *domain.Payment) {
	if !s.groupBookingPing(b.Source) {
		return
	}
	payload := s.waPayload(b, p)
	payload["band"] = bandLabel(b)
	if _, err := s.opts.Wa.Enqueue(ctx, s.opts.StaffGroup, "staff_booking", payload, &b.ID,
		"booking:"+b.ID.String()+":staff", time.Time{}); err != nil {
		s.log.Warn("wa enqueue staff booking", "booking", b.ID, "err", err)
	}
}

// notifyGroupSlot tells the staff group that a slot opened up or moved. These
// always fire (unless pings are off): a freed slot is actionable whoever
// released it, and a reschedule changes the bidan's day.
func (s *BookingService) notifyGroupSlot(ctx context.Context, b *domain.Booking, kind, actor string, oldStart time.Time) {
	if !s.groupEnabled() {
		return
	}
	key := "staff_cancelled"
	if kind == "rescheduled" {
		key = "staff_rescheduled"
	}
	payload := s.waPayload(b, nil)
	payload["label"] = slotLabel(kind)
	payload["actor"] = actor
	if !oldStart.IsZero() {
		payload["old_when"] = formatWIB(oldStart)
	}
	dedupe := fmt.Sprintf("slot:%s:%s:%d", b.ID.String(), kind, time.Now().UnixNano())
	if _, err := s.opts.Wa.Enqueue(ctx, s.opts.StaffGroup, key, payload, &b.ID, dedupe, time.Time{}); err != nil {
		s.log.Warn("wa enqueue staff slot", "booking", b.ID, "kind", kind, "err", err)
	}
}

// actorLabel names who caused a transition, for the staff copy.
func actorLabel(actorID *uuid.UUID) string {
	if actorID == nil {
		return "customer"
	}
	return "staf"
}

// slotLabel is the headline of a slot ping.
func slotLabel(kind string) string {
	switch kind {
	case "cancelled":
		return "BATAL"
	case "expired":
		return "KEDALUWARSA"
	case "no_show":
		return "NO-SHOW"
	case "rescheduled":
		return "JADWAL BERUBAH"
	}
	return strings.ToUpper(kind)
}

// bandLabel renders the chosen transport band ("≤5 km") for staff copy.
func bandLabel(b *domain.Booking) string {
	if b.DistanceKm == nil {
		return ""
	}
	return "≤" + strconv.FormatFloat(*b.DistanceKm, 'g', -1, 64) + " km"
}

// waPayload builds the common message payload. p may be nil for messages that
// only need booking details.
func (s *BookingService) waPayload(b *domain.Booking, p *domain.Payment) map[string]any {
	name := b.CustomerName
	phone := b.CustomerPhone
	if b.IsGuest() {
		name = b.GuestName
		phone = b.GuestPhone
	}
	out := map[string]any{
		"name":       displayName(name),
		"phone":      phone,
		"code":       orderRef(b.ID),
		"service":    b.ServiceName,
		"staff":      b.StaffName,
		"when":       formatWIB(b.StartAt),
		"location":   b.LocationName,
		"address":    "",
		"band":       "",
		"source":     b.Source,
		"amount":     b.Net(),
		"paid":       false,
		"pay_url":    s.payURL(b),
		"book_url":   s.baseURL + "/book",
		"method":     "QRIS",
		"paid_at":    "",
		"portal_url": s.baseURL + "/portal/bookings/" + b.ID.String(),
		"note":       "",
	}
	if b.Place == domain.PlaceHomecare {
		out["address"] = b.VisitAddress
	}
	if p != nil {
		out["code"] = p.OrderID
		out["amount"] = p.AmountIDR
		out["paid"] = p.Status == domain.PaymentPaid
		out["method"] = methodLabel(p.PaymentMethod)
		out["note"] = p.ReviewNote
		if p.PaidAt != nil {
			out["paid_at"] = formatWIB(*p.PaidAt)
		}
	}
	return out
}

// orderRef is the customer-facing booking reference shown on the pay page and
// in every message. It comes from the booking id, so lifecycle messages can
// quote the same code without loading the payment row.
func orderRef(id uuid.UUID) string {
	return fmt.Sprintf("SPA-%s", strings.ReplaceAll(id.String(), "-", "")[:16])
}

func displayName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Bunda"
	}
	return name
}

func methodLabel(m domain.PaymentMethod) string {
	switch m {
	case domain.PaymentMethodCash:
		return "Tunai"
	case domain.PaymentMethodQRIS, domain.PaymentMethodQRISStatic:
		return "QRIS"
	}
	return string(m)
}

// formatWIB renders a timestamp in the spa's local time, in Indonesian.
func formatWIB(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	t = t.In(wib)
	return fmt.Sprintf("%s, %d %s %d · %s WIB",
		wibDays[int(t.Weekday())], t.Day(), wibMonths[int(t.Month())-1], t.Year(), t.Format("15:04"))
}

// formatDayWIB renders a date in Indonesian, without the time.
func formatDayWIB(t time.Time) string {
	t = t.In(wib)
	return fmt.Sprintf("%s, %d %s %d", wibDays[int(t.Weekday())], t.Day(), wibMonths[int(t.Month())-1], t.Year())
}

var (
	wibDays   = [...]string{"Minggu", "Senin", "Selasa", "Rabu", "Kamis", "Jumat", "Sabtu"}
	wibMonths = [...]string{"Januari", "Februari", "Maret", "April", "Mei", "Juni", "Juli", "Agustus", "September", "Oktober", "November", "Desember"}
)
