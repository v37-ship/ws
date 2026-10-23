package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
)

// Notifier is the outbox surface used for transactional WhatsApp messages.
// WaService implements it; tests can fake it.
type Notifier interface {
	Enqueue(ctx context.Context, phone, templateKey string, payload map[string]any, bookingID *uuid.UUID, dedupeKey string, scheduledAt time.Time) (bool, error)
}

// BookingOptions carries the deployment switches for payment and messaging.
type BookingOptions struct {
	PaymentMode string // "qris_manual" or "midtrans"
	Wa          Notifier
	CustomerWA  bool   // queue automatic customer messages
	StaffGroup  string // Evolution group JID for staff alerts
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

// waPayload builds the common message payload. p may be nil for messages that
// only need booking details.
func (s *BookingService) waPayload(b *domain.Booking, p *domain.Payment) map[string]any {
	name := b.CustomerName
	if b.IsGuest() {
		name = b.GuestName
	}
	out := map[string]any{
		"name":       displayName(name),
		"code":       b.ID.String(),
		"service":    b.ServiceName,
		"staff":      b.StaffName,
		"when":       formatWIB(b.StartAt),
		"location":   b.LocationName,
		"address":    "",
		"amount":     b.Net(),
		"paid":       false,
		"pay_url":    s.payURL(b),
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
	t = t.In(time.FixedZone("WIB", 7*3600))
	days := [...]string{"Minggu", "Senin", "Selasa", "Rabu", "Kamis", "Jumat", "Sabtu"}
	months := [...]string{"Januari", "Februari", "Maret", "April", "Mei", "Juni", "Juli", "Agustus", "September", "Oktober", "November", "Desember"}
	return fmt.Sprintf("%s, %d %s %d · %s WIB",
		days[int(t.Weekday())], t.Day(), months[int(t.Month())-1], t.Year(), t.Format("15:04"))
}
