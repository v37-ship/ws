package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
)

// activeBooking reports whether a booking may still collect a payment.
func activeBooking(status domain.BookingStatus) bool {
	switch status {
	case domain.BookingConfirmed, domain.BookingCheckedIn, domain.BookingInService,
		domain.BookingCompleted, domain.BookingPendingPayment:
		return true
	}
	return false
}

// ClaimPaid records "Saya sudah bayar" from the customer and pings the staff
// group. Returns ErrValidation when the payment is already settled.
func (s *BookingService) ClaimPaid(ctx context.Context, bookingID uuid.UUID, actorID *uuid.UUID) error {
	b, err := s.bookings.ByID(ctx, bookingID)
	if err != nil {
		return err
	}
	if !activeBooking(b.Status) {
		return fmt.Errorf("%w: this booking can no longer accept a payment", ErrValidation)
	}
	ok, err := s.payments.RequestVerification(ctx, bookingID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: payment is already settled", ErrValidation)
	}
	_ = s.bookings.LogActivity(ctx, bookingID, actorID, "Customer reported payment (awaiting staff verification)", "")
	p, _ := s.payments.ByBookingID(ctx, bookingID)
	if p != nil {
		s.notifyStaffClaim(ctx, b, p)
	}
	return nil
}

// ConfirmManualPayment marks a pending payment as received. It is the single
// staff action for both static QRIS and cash at the counter.
func (s *BookingService) ConfirmManualPayment(ctx context.Context, bookingID uuid.UUID, method domain.PaymentMethod, reference, note string, actorID uuid.UUID) error {
	if method != domain.PaymentMethodQRISStatic && method != domain.PaymentMethodCash {
		return fmt.Errorf("%w: unsupported manual payment method", ErrValidation)
	}
	b, err := s.bookings.ByID(ctx, bookingID)
	if err != nil {
		return err
	}
	if !activeBooking(b.Status) {
		return fmt.Errorf("%w: this booking can no longer accept a payment", ErrValidation)
	}
	p, err := s.payments.ByBookingID(ctx, bookingID)
	if err != nil {
		return err
	}
	if p.Status == domain.PaymentPaid {
		return fmt.Errorf("%w: payment is already confirmed", ErrValidation)
	}
	if method == domain.PaymentMethodCash && p.PaymentPurpose == domain.PaymentPurposeBookingFee {
		return fmt.Errorf("%w: cash cannot be used for a booking fee", ErrValidation)
	}
	ok, err := s.payments.MarkPaidManual(ctx, bookingID, method, reference, note, actorID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: payment is already confirmed", ErrValidation)
	}
	if b.Status == domain.BookingPendingPayment {
		if err := s.UpdateStatus(ctx, bookingID, domain.BookingConfirmed, &actorID, "Payment confirmed manually"); err != nil {
			return err
		}
	}
	_ = s.bookings.LogActivity(ctx, bookingID, &actorID,
		fmt.Sprintf("Manual payment confirmed (%s)", methodLabel(method)), strings.TrimSpace(reference+" "+note))

	full, _ := s.bookings.ByID(ctx, bookingID)
	if full != nil {
		b = full
	}
	p, _ = s.payments.ByBookingID(ctx, bookingID)
	if p != nil {
		name, phone := s.contact(ctx, b)
		s.notifyReceipt(ctx, b, p, name, phone)
	}
	return nil
}

// RejectClaim clears a paid claim after staff could not match the transfer.
func (s *BookingService) RejectClaim(ctx context.Context, bookingID uuid.UUID, note string, actorID uuid.UUID) error {
	b, err := s.bookings.ByID(ctx, bookingID)
	if err != nil {
		return err
	}
	p, err := s.payments.ByBookingID(ctx, bookingID)
	if err != nil {
		return err
	}
	if p.Status == domain.PaymentPaid {
		return fmt.Errorf("%w: payment is already confirmed", ErrValidation)
	}
	if p.CustomerReportedAt == nil {
		return fmt.Errorf("%w: there is no payment claim to reject", ErrValidation)
	}
	if err := s.payments.ClearVerification(ctx, bookingID, note); err != nil {
		return err
	}
	_ = s.bookings.LogActivity(ctx, bookingID, &actorID, "Payment claim rejected", strings.TrimSpace(note))
	_, phone := s.contact(ctx, b)
	s.notifyClaimRejected(ctx, b, phone)
	return nil
}

// SetReceipt stores the transfer proof path on the booking's payment.
func (s *BookingService) SetReceipt(ctx context.Context, bookingID uuid.UUID, path string, actorID *uuid.UUID) error {
	if err := s.payments.SetReceipt(ctx, bookingID, path); err != nil {
		return err
	}
	_ = s.bookings.LogActivity(ctx, bookingID, actorID, "Payment receipt uploaded", "")
	return nil
}

// PaymentCounts returns outstanding payments and claims awaiting review.
func (s *BookingService) PaymentCounts(ctx context.Context) (unpaid, claims int, err error) {
	return s.payments.Counts(ctx)
}

// SendManualWA queues an explicit staff-initiated WhatsApp message. Manual
// sends bypass the automatic-customer switch: a staff member pressing the
// button is the intent. kind is "confirm", "receipt" or "text".
func (s *BookingService) SendManualWA(ctx context.Context, bookingID uuid.UUID, kind, text string, actorID uuid.UUID) error {
	if s.opts.Wa == nil {
		return fmt.Errorf("%w: WhatsApp is not configured", ErrValidation)
	}
	b, err := s.bookings.ByID(ctx, bookingID)
	if err != nil {
		return err
	}
	name, phone := s.contact(ctx, b)
	if strings.TrimSpace(phone) == "" {
		return fmt.Errorf("%w: this booking has no WhatsApp number", ErrValidation)
	}
	p, _ := s.payments.ByBookingID(ctx, bookingID)

	var key string
	var payload map[string]any
	switch kind {
	case "confirm":
		key = "booking_confirm"
		payload = s.waPayload(b, p)
		payload["name"] = displayName(name)
	case "remind":
		key = "booking_remind"
		payload = s.waPayload(b, p)
		payload["name"] = displayName(name)
	case "receipt":
		if p == nil || p.Status != domain.PaymentPaid {
			return fmt.Errorf("%w: payment is not settled yet", ErrValidation)
		}
		key = "payment_receipt"
		payload = s.waPayload(b, p)
		payload["name"] = displayName(name)
	case "text":
		text = strings.TrimSpace(text)
		if n := len([]rune(text)); n < 1 || n > 700 {
			return fmt.Errorf("%w: message must be 1-700 characters", ErrValidation)
		}
		key = "text"
		payload = map[string]any{"text": text}
	default:
		return fmt.Errorf("%w: unknown message kind", ErrValidation)
	}

	dedupe := fmt.Sprintf("manual:%s:%s:%d", kind, bookingID.String(), time.Now().UnixNano())
	if _, err := s.opts.Wa.Enqueue(ctx, phone, key, payload, &bookingID, dedupe, time.Time{}); err != nil {
		return err
	}
	_ = s.bookings.LogActivity(ctx, bookingID, &actorID, "WhatsApp "+kind+" queued by staff", "")
	return nil
}

// contact resolves the customer's display name and WhatsApp number.
func (s *BookingService) contact(ctx context.Context, b *domain.Booking) (name, phone string) {
	if b.CustomerID != nil {
		if u, err := s.users.ByID(ctx, *b.CustomerID); err == nil {
			return u.FullName, u.Phone
		}
	}
	return b.GuestContactName(), b.GuestPhone
}
