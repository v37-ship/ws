package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"spa-booking/internal/domain"
	"spa-booking/internal/email"
	"spa-booking/internal/payment"
	"spa-booking/internal/repo"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

type BookingService struct {
	bookings *repo.BookingRepo
	payments *repo.PaymentRepo
	catalog  *repo.CatalogRepo
	users    *repo.UserRepo
	promos   *repo.PromoRepo
	zones    *repo.DistanceRepo
	avail    *AvailabilityService
	midtrans *payment.Client
	mailer   *email.Sender
	holdTTL  time.Duration
	baseURL  string
	log      *slog.Logger
	opts     BookingOptions
	settings *WaSettingsService
	now      func() time.Time // test seam; nil = wall clock
}

func NewBookingService(
	bookings *repo.BookingRepo,
	payments *repo.PaymentRepo,
	catalog *repo.CatalogRepo,
	users *repo.UserRepo,
	promos *repo.PromoRepo,
	zones *repo.DistanceRepo,
	avail *AvailabilityService,
	midtrans *payment.Client,
	mailer *email.Sender,
	holdTTL time.Duration,
	baseURL string,
	log *slog.Logger,
	opts BookingOptions,
) *BookingService {
	return &BookingService{
		bookings: bookings, payments: payments, catalog: catalog, users: users, promos: promos,
		zones: zones, avail: avail, midtrans: midtrans, mailer: mailer, holdTTL: holdTTL, baseURL: baseURL, log: log,
		opts: opts, settings: opts.Settings,
	}
}

func (s *BookingService) CreatePending(ctx context.Context, customerID, locationID, serviceID, staffID uuid.UUID, startAt time.Time, notes string, place domain.Place, visitAddress string, promoCode string, distanceZoneID *uuid.UUID) (*domain.Booking, *domain.Payment, error) {
	svc, err := s.catalog.ServiceByID(ctx, serviceID)
	if err != nil {
		return nil, nil, err
	}
	if svc.LocationID != locationID {
		return nil, nil, fmt.Errorf("%w: service/location mismatch", ErrValidation)
	}
	place, visitAddress, err = normalizePlace(place, visitAddress)
	if err != nil {
		return nil, nil, err
	}
	st, err := s.catalog.StaffByID(ctx, staffID)
	if err != nil {
		return nil, nil, err
	}
	if st.LocationID != locationID {
		return nil, nil, fmt.Errorf("%w: staff/location mismatch", ErrValidation)
	}
	customer, err := s.users.ByID(ctx, customerID)
	if err != nil {
		return nil, nil, err
	}
	if !customer.IsActive || customer.Role != domain.RoleCustomer {
		return nil, nil, fmt.Errorf("%w: customer account is deactivated", ErrValidation)
	}
	quote, err := QuotePromo(ctx, s.promos, promoCode, svc.PriceIDR)
	if err != nil {
		return nil, nil, err
	}
	distanceFee, distanceKm, err := ResolveDistance(ctx, s.zones, place, distanceZoneID)
	if err != nil {
		return nil, nil, err
	}
	endAt := startAt.Add(time.Duration(svc.DurationMin) * time.Minute)
	holdExp := time.Now().Add(s.holdTTL)
	b := &domain.Booking{
		CustomerID:     &customerID,
		LocationID:     locationID,
		ServiceID:      serviceID,
		StaffID:        staffID,
		StartAt:        startAt.UTC(),
		EndAt:          endAt.UTC(),
		Status:         domain.BookingPendingPayment,
		PriceIDR:       svc.PriceIDR,
		PromoCode:      quote.Code,
		DiscountIDR:    quote.Discount,
		HoldExpiresAt:  &holdExp,
		Notes:          strings.TrimSpace(notes),
		Place:          place,
		VisitAddress:   visitAddress,
		DistanceKm:     distanceKm,
		DistanceFeeIDR: distanceFee,
		Source:         "web",
	}
	manual := s.ManualPayments()
	if manual {
		// Pay-later: the slot is secured now, payment happens online or at
		// the counter after the service.
		b.Status = domain.BookingConfirmed
		b.HoldExpiresAt = nil
	}
	if err := s.bookings.Create(ctx, b); err != nil {
		if isExclusionViolation(err) {
			return nil, nil, ErrConflict
		}
		return nil, nil, err
	}
	orderID := orderRef(b.ID)
	var charge *payment.QRISChargeResult
	err = runRedeemedCharge(
		func() error {
			if quote.Code == "" {
				return nil
			}
			if err := s.promos.TryRedeem(ctx, quote.Code); err != nil {
				return fmt.Errorf("%w: promo code just reached its limit", ErrValidation)
			}
			return nil
		},
		func() error {
			if manual {
				return nil
			}
			c, err := s.midtrans.ChargeQRIS(ctx, payment.QRISChargeRequest{
				OrderID:      orderID,
				AmountIDR:    b.Net(),
				CustomerName: customer.FullName,
				Email:        customer.Email,
				ItemName:     svc.Name,
			})
			if err != nil {
				return fmt.Errorf("qris charge: %w", err)
			}
			charge = c
			return nil
		},
		func() error {
			if quote.Code == "" {
				return nil
			}
			return s.promos.Unredeem(ctx, quote.Code)
		},
	)
	if err != nil {
		_ = s.bookings.UpdateStatus(ctx, b.ID, domain.BookingExpired)
		return nil, nil, err
	}
	var p *domain.Payment
	if manual {
		p = &domain.Payment{
			BookingID:      b.ID,
			OrderID:        orderID,
			AmountIDR:      b.Net(),
			Status:         domain.PaymentPending,
			PaymentMethod:  domain.PaymentMethodQRISStatic,
			PaymentPurpose: domain.PaymentPurposeFull,
		}
		if err := s.payments.Create(ctx, p); err != nil {
			return nil, nil, err
		}
		_ = s.bookings.LogActivity(ctx, b.ID, &customerID, "Booking created online (manual QRIS pending)", notes)
		logCreationStatus(ctx, s.bookings, b, &customerID)
	} else {
		p = &domain.Payment{
			BookingID:       b.ID,
			OrderID:         orderID,
			AmountIDR:       b.Net(),
			Status:          domain.PaymentPending,
			PaymentMethod:   domain.PaymentMethodQRIS,
			PaymentPurpose:  domain.PaymentPurposeFull,
			QRString:        charge.QRString,
			MidtransTransID: charge.TransactionID,
		}
		if err := s.payments.Create(ctx, p); err != nil {
			return nil, nil, err
		}
		_ = s.bookings.LogActivity(ctx, b.ID, &customerID, "Booking created online (QRIS pending)", notes)
		logCreationStatus(ctx, s.bookings, b, &customerID)
	}
	full, _ := s.bookings.ByID(ctx, b.ID)
	if full != nil {
		b = full
	}
	if manual {
		s.onConfirmed(ctx, b, p, customer.FullName, customer.Phone)
	}
	return b, p, nil
}

// runRedeemedCharge applies the online booking money rule: a promo redemption
// is charged in the same step, and a failed charge rolls the redemption back.
// A failed redemption stops the sequence before the charge runs; the returned
// error is the one that stopped it (nil when both steps succeed).
func runRedeemedCharge(redeem func() error, charge func() error, unredeem func() error) error {
	if err := redeem(); err != nil {
		return err
	}
	if err := charge(); err != nil {
		_ = unredeem()
		return err
	}
	return nil
}

// normalizePlace validates the fulfillment place. Empty defaults to onsite;
// homecare requires a visit address (min 10 chars). Onsite drops any address.
func normalizePlace(place domain.Place, visitAddress string) (domain.Place, string, error) {
	if place == "" {
		place = domain.PlaceOnsite
	}
	if place != domain.PlaceOnsite && place != domain.PlaceHomecare {
		return "", "", fmt.Errorf("%w: unknown place", ErrValidation)
	}
	addr := strings.TrimSpace(visitAddress)
	if place == domain.PlaceHomecare {
		if len([]rune(addr)) < 10 {
			return "", "", fmt.Errorf("%w: homecare requires a visit address", ErrValidation)
		}
		return place, addr, nil
	}
	return place, "", nil
}

type StaffBookingRequest struct {
	CustomerID     uuid.UUID
	LocationID     uuid.UUID
	ServiceID      uuid.UUID
	StaffID        uuid.UUID
	StartAt        time.Time
	Notes          string
	Place          domain.Place
	VisitAddress   string
	Source         string                // 'walkin', 'whatsapp', 'phone'
	PaymentMethod  domain.PaymentMethod  // 'qris' or 'cash'
	PaymentPurpose domain.PaymentPurpose // 'service' or 'booking_fee' or 'full'
	PromoCode      string                // optional promo code (validated server-side)
	DistanceZoneID *uuid.UUID            // homecare transport band (nil = onsite/none)
	ActorID        uuid.UUID
}

// CreateStaffBooking handles CS and Admin registrations on behalf of customers.
// Enforces: Cash CANNOT be used for booking fees, only for services!
func (s *BookingService) CreateStaffBooking(ctx context.Context, req StaffBookingRequest) (*domain.Booking, *domain.Payment, error) {
	if req.PaymentMethod == domain.PaymentMethodCash && req.PaymentPurpose == domain.PaymentPurposeBookingFee {
		return nil, nil, fmt.Errorf("%w: cash cannot be used for booking fee, cash is only valid for services", ErrValidation)
	}
	if req.PaymentMethod == "" {
		req.PaymentMethod = domain.PaymentMethodQRIS
	}
	if req.PaymentPurpose == "" {
		req.PaymentPurpose = domain.PaymentPurposeService
	}
	if req.Source == "" {
		req.Source = "walkin"
	}
	place, visitAddress, err := normalizePlace(req.Place, req.VisitAddress)
	if err != nil {
		return nil, nil, err
	}

	svc, err := s.catalog.ServiceByID(ctx, req.ServiceID)
	if err != nil {
		return nil, nil, err
	}
	if svc.LocationID != req.LocationID {
		return nil, nil, fmt.Errorf("%w: service/location mismatch", ErrValidation)
	}
	st, err := s.catalog.StaffByID(ctx, req.StaffID)
	if err != nil {
		return nil, nil, err
	}
	if st.LocationID != req.LocationID {
		return nil, nil, fmt.Errorf("%w: staff/location mismatch", ErrValidation)
	}
	customer, err := s.users.ByID(ctx, req.CustomerID)
	if err != nil {
		return nil, nil, err
	}
	if !customer.IsActive || customer.Role != domain.RoleCustomer {
		return nil, nil, fmt.Errorf("%w: customer account is deactivated", ErrValidation)
	}

	endAt := req.StartAt.Add(time.Duration(svc.DurationMin) * time.Minute)
	quote, err := QuotePromo(ctx, s.promos, req.PromoCode, svc.PriceIDR)
	if err != nil {
		return nil, nil, err
	}
	distanceFee, distanceKm, err := ResolveDistance(ctx, s.zones, place, req.DistanceZoneID)
	if err != nil {
		return nil, nil, err
	}
	b := &domain.Booking{
		CustomerID:     &req.CustomerID,
		LocationID:     req.LocationID,
		ServiceID:      req.ServiceID,
		StaffID:        req.StaffID,
		StartAt:        req.StartAt.UTC(),
		EndAt:          endAt.UTC(),
		PriceIDR:       svc.PriceIDR,
		PromoCode:      quote.Code,
		DiscountIDR:    quote.Discount,
		Notes:          strings.TrimSpace(req.Notes),
		Place:          place,
		VisitAddress:   visitAddress,
		DistanceKm:     distanceKm,
		DistanceFeeIDR: distanceFee,
		Source:         req.Source,
		CreatedByID:    &req.ActorID,
		UpdatedByID:    &req.ActorID,
	}

	orderID := fmt.Sprintf("STAFF-%s", strings.ReplaceAll(uuid.New().String(), "-", "")[:14])
	var p *domain.Payment

	if req.PaymentMethod == domain.PaymentMethodCash && req.PaymentPurpose == domain.PaymentPurposeBookingFee {
		return nil, nil, fmt.Errorf("%w: cash cannot be used for a booking fee", ErrValidation)
	}
	if req.PaymentMethod == domain.PaymentMethodCash {
		// Cash paid on-site for services: confirmed immediately
		b.Status = domain.BookingConfirmed
		b.HoldExpiresAt = nil
		if err := s.bookings.Create(ctx, b); err != nil {
			if isExclusionViolation(err) {
				return nil, nil, ErrConflict
			}
			return nil, nil, err
		}
		if quote.Code != "" {
			if err := s.promos.TryRedeem(ctx, quote.Code); err != nil {
				_ = s.bookings.UpdateStatus(ctx, b.ID, domain.BookingExpired)
				return nil, nil, fmt.Errorf("%w: promo code just reached its limit", ErrValidation)
			}
		}
		p = &domain.Payment{
			BookingID:      b.ID,
			OrderID:        orderID,
			AmountIDR:      b.Net(),
			Status:         domain.PaymentPaid,
			PaymentMethod:  domain.PaymentMethodCash,
			PaymentPurpose: req.PaymentPurpose,
		}
		if err := s.payments.CreateCashPayment(ctx, p); err != nil {
			return nil, nil, err
		}
		_ = s.bookings.LogActivity(ctx, b.ID, &req.ActorID, "Booking created by CS (Cash Payment - Services)", req.Notes)
		logCreationStatus(ctx, s.bookings, b, &req.ActorID)
	} else if s.ManualPayments() {
		b.Status = domain.BookingConfirmed
		b.HoldExpiresAt = nil
		if err := s.bookings.Create(ctx, b); err != nil {
			if isExclusionViolation(err) {
				return nil, nil, ErrConflict
			}
			return nil, nil, err
		}
		if quote.Code != "" {
			if err := s.promos.TryRedeem(ctx, quote.Code); err != nil {
				_ = s.bookings.UpdateStatus(ctx, b.ID, domain.BookingExpired)
				return nil, nil, fmt.Errorf("%w: promo code just reached its limit", ErrValidation)
			}
		}
		p = &domain.Payment{
			BookingID:      b.ID,
			OrderID:        orderID,
			AmountIDR:      b.Net(),
			Status:         domain.PaymentPending,
			PaymentMethod:  domain.PaymentMethodQRISStatic,
			PaymentPurpose: req.PaymentPurpose,
		}
		if err := s.payments.Create(ctx, p); err != nil {
			return nil, nil, err
		}
		_ = s.bookings.LogActivity(ctx, b.ID, &req.ActorID, "Booking created by CS (manual QRIS pending)", req.Notes)
		logCreationStatus(ctx, s.bookings, b, &req.ActorID)
	} else {
		// QRIS: can pay booking fee or service
		holdExp := time.Now().Add(s.holdTTL)
		b.Status = domain.BookingPendingPayment
		b.HoldExpiresAt = &holdExp
		if err := s.bookings.Create(ctx, b); err != nil {
			if isExclusionViolation(err) {
				return nil, nil, ErrConflict
			}
			return nil, nil, err
		}
		redeemed := false
		if quote.Code != "" {
			if err := s.promos.TryRedeem(ctx, quote.Code); err != nil {
				_ = s.bookings.UpdateStatus(ctx, b.ID, domain.BookingExpired)
				return nil, nil, fmt.Errorf("%w: promo code just reached its limit", ErrValidation)
			}
			redeemed = true
		}
		charge, err := s.midtrans.ChargeQRIS(ctx, payment.QRISChargeRequest{
			OrderID:      orderID,
			AmountIDR:    b.Net(),
			CustomerName: customer.FullName,
			Email:        customer.Email,
			ItemName:     svc.Name,
		})
		if err != nil {
			if redeemed {
				_ = s.promos.Unredeem(ctx, quote.Code)
			}
			_ = s.bookings.UpdateStatus(ctx, b.ID, domain.BookingExpired)
			return nil, nil, fmt.Errorf("qris charge: %w", err)
		}
		p = &domain.Payment{
			BookingID:       b.ID,
			OrderID:         orderID,
			AmountIDR:       b.Net(),
			Status:          domain.PaymentPending,
			PaymentMethod:   domain.PaymentMethodQRIS,
			PaymentPurpose:  req.PaymentPurpose,
			QRString:        charge.QRString,
			MidtransTransID: charge.TransactionID,
		}
		if err := s.payments.Create(ctx, p); err != nil {
			return nil, nil, err
		}
		_ = s.bookings.LogActivity(ctx, b.ID, &req.ActorID, "Booking created by CS (QRIS pending)", req.Notes)
		logCreationStatus(ctx, s.bookings, b, &req.ActorID)
	}

	full, _ := s.bookings.ByID(ctx, b.ID)
	if full != nil {
		b = full
	}
	if p != nil {
		switch {
		case p.Status == domain.PaymentPaid:
			// Cash walk-in: the receipt is the confirmation, so it is not sent
			// twice — but the reminder and the staff ping still apply.
			s.notifyReceipt(ctx, b, p, customer.FullName, customer.Phone)
			s.scheduleReminder(ctx, b, customer.FullName, customer.Phone)
			s.notifyGroupBooking(ctx, b, p)
		case s.ManualPayments():
			s.onConfirmed(ctx, b, p, customer.FullName, customer.Phone)
		}
	}
	return b, p, nil
}

// statusLogPrefix is the action text BookingRepo.UpdateStatusWithLog writes for
// every status change. The audit log stores only the new status — there is no
// previous-status column — so undo derives the previous one from this chain.
const statusLogPrefix = domain.StatusLogPrefix

// statusLogWriter is the slice of BookingRepo the creation trail needs: the
// same activity-row seam every other audit entry goes through.
type statusLogWriter interface {
	LogActivity(ctx context.Context, bookingID uuid.UUID, actorID *uuid.UUID, action, notes string) error
}

// logCreationStatus records the status a booking was created with, in the exact
// form every later transition writes. Without it a booking's first manual move
// (a walk-in's check-in, typically) has no predecessor for undo to return to:
// the creation paths log a human-readable "Booking created…" row, which
// previousStatusFromLogs skips. Best-effort like the surrounding activity rows —
// a failed audit insert must not fail a booking that already exists.
func logCreationStatus(ctx context.Context, logs statusLogWriter, b *domain.Booking, actorID *uuid.UUID) {
	_ = logs.LogActivity(ctx, b.ID, actorID, domain.StatusLogAction(b.Status), "")
}

// previousStatusFromLogs derives the status a booking was in before its most
// recent status change. Entries arrive newest-first (BookingRepo.ListLogs).
//
// The newest status entry must equal current: if anything moved the booking
// since, the chain no longer matches and undo must refuse rather than overwrite
// a concurrent change. Non-status entries interleaved in the log (notes,
// reschedules) are skipped, and a lone status entry has no predecessor.
func previousStatusFromLogs(entries []domain.BookingLog, current domain.BookingStatus) (domain.BookingStatus, bool) {
	matched := false
	for _, e := range entries {
		if !strings.HasPrefix(e.Action, statusLogPrefix) {
			continue
		}
		status := domain.BookingStatus(strings.TrimSpace(strings.TrimPrefix(e.Action, statusLogPrefix)))
		if !knownBookingStatus(status) {
			continue
		}
		if !matched {
			if status != current {
				return "", false
			}
			matched = true
			continue
		}
		return status, true
	}
	return "", false
}

// knownBookingStatus reports whether s is one of the eight lifecycle statuses.
func knownBookingStatus(s domain.BookingStatus) bool {
	switch s {
	case domain.BookingPendingPayment, domain.BookingConfirmed, domain.BookingCheckedIn, domain.BookingInService,
		domain.BookingCompleted, domain.BookingCancelled, domain.BookingExpired, domain.BookingNoShow:
		return true
	}
	return false
}

func (s *BookingService) UpdateStatus(ctx context.Context, bookingID uuid.UUID, newStatus domain.BookingStatus, actorID *uuid.UUID, note string) error {
	b, err := s.bookings.ByID(ctx, bookingID)
	if err != nil {
		return err
	}
	if !knownBookingStatus(newStatus) {
		return fmt.Errorf("%w: unknown booking status %s", ErrValidation, newStatus)
	}
	return s.applyStatus(ctx, b, newStatus, actorID, note, true)
}

// UndoLastStatus reverts the booking's most recent status change. It is
// deliberately quiet toward the customer (ruling 3): the reverted transition
// runs no lifecycle fan-out, so it never sends a new message. Un-finishing a
// visit drops the pending +2h review request, because a review ask for an
// unfinished visit is wrong. Un-cancelling sends nothing — the original
// confirmation dedupe key is permanent — so the handler's toast tells staff to
// resend the confirmation from the detail page instead.
func (s *BookingService) UndoLastStatus(ctx context.Context, bookingID uuid.UUID, actorID *uuid.UUID) (domain.BookingStatus, error) {
	b, err := s.bookings.ByID(ctx, bookingID)
	if err != nil {
		return "", err
	}
	logs, err := s.bookings.ListLogs(ctx, bookingID)
	if err != nil {
		return "", err
	}
	prev, ok := previousStatusFromLogs(logs, b.Status)
	if !ok {
		return "", fmt.Errorf("%w: no undoable status change", ErrValidation)
	}
	if err := s.applyStatus(ctx, b, prev, actorID, "Undo: restored from "+string(b.Status), false); err != nil {
		return "", err
	}
	if b.Status == domain.BookingCompleted {
		s.cancelReview(ctx, bookingID)
	}
	return prev, nil
}

// applyStatus writes the status and its audit row, then (when fanOut is true)
// runs the lifecycle side effects. Every forward transition funnels through
// here so the customer and the staff group hear about it exactly once, and so
// scheduled reminders never outlive the booking they belong to. Undo passes
// false: a revert is a correction, not a new event.
func (s *BookingService) applyStatus(ctx context.Context, b *domain.Booking, newStatus domain.BookingStatus, actorID *uuid.UUID, note string, fanOut bool) error {
	if err := s.bookings.UpdateStatusWithLog(ctx, b.ID, newStatus, actorID, note); err != nil {
		return err
	}
	if !fanOut {
		return nil
	}
	switch newStatus {
	case domain.BookingCancelled:
		s.cancelReminder(ctx, b.ID)
		s.notifyCustomerCancelled(ctx, b, note)
		s.notifyGroupSlot(ctx, b, "cancelled", actorLabel(actorID), time.Time{})
		if email, name := s.bookingRecipient(ctx, b); email != "" {
			_ = s.mailer.Send(email, "Wangga booking cancelled",
				fmt.Sprintf("Hi %s, your Wangga booking for %s on %s was cancelled. Note: %s", name, b.ServiceName, b.StartAt.Format(time.RFC1123), note))
		}
	case domain.BookingExpired:
		s.cancelReminder(ctx, b.ID)
		s.notifyCustomerExpired(ctx, b)
		s.notifyGroupSlot(ctx, b, "expired", "sistem", time.Time{})
	case domain.BookingNoShow:
		s.cancelReminder(ctx, b.ID)
		s.notifyGroupSlot(ctx, b, "no_show", actorLabel(actorID), time.Time{})
	case domain.BookingCompleted:
		s.notifyReviewRequest(ctx, b)
	case domain.BookingConfirmed:
		// Reload as confirmed: the confirmation message, the H-1 reminder and
		// the group ping all key off the new status.
		bb := *b
		bb.Status = domain.BookingConfirmed
		p, _ := s.payments.ByBookingID(ctx, b.ID)
		name, phone := s.contact(ctx, &bb)
		s.onConfirmed(ctx, &bb, p, name, phone)
	}
	return nil
}

// bookingRecipient resolves where booking mail goes: the account email, or
// the guest address when present. Empty email means do not send.
func (s *BookingService) bookingRecipient(ctx context.Context, b *domain.Booking) (string, string) {
	if b.CustomerID != nil {
		if customer, err := s.users.ByID(ctx, *b.CustomerID); err == nil {
			return customer.Email, customer.FullName
		}
		return "", ""
	}
	return b.GuestEmail, b.GuestContactName()
}

func (s *BookingService) Reschedule(ctx context.Context, bookingID, staffID uuid.UUID, startAt time.Time, actorID *uuid.UUID, note string) error {
	if startAt.Before(time.Now()) {
		return fmt.Errorf("%w: cannot reschedule to the past", ErrValidation)
	}
	b, err := s.bookings.ByID(ctx, bookingID)
	if err != nil {
		return err
	}
	svc, err := s.catalog.ServiceByID(ctx, b.ServiceID)
	if err != nil {
		return err
	}
	if staffID == uuid.Nil {
		staffID = b.StaffID
	}
	st, err := s.catalog.StaffByID(ctx, staffID)
	if err != nil {
		return err
	}
	if st.LocationID != b.LocationID {
		return fmt.Errorf("%w: staff/location mismatch", ErrValidation)
	}
	endAt := startAt.Add(time.Duration(svc.DurationMin) * time.Minute)

	if err := s.bookings.RescheduleWithLog(ctx, bookingID, staffID, startAt.UTC(), endAt.UTC(), actorID, note); err != nil {
		if isExclusionViolation(err) {
			return ErrConflict
		}
		return err
	}

	if email, name := s.bookingRecipient(ctx, b); email != "" {
		_ = s.mailer.Send(email, "Wangga booking rescheduled",
			fmt.Sprintf("Hi %s, your Wangga booking for %s has been rescheduled to %s. Note: %s",
				name, b.ServiceName, startAt.Format(time.RFC1123), note))
	}

	// The reminder travels with the booking, and both the customer and the
	// group are told about the new time.
	oldStart := b.StartAt
	if full, err := s.bookings.ByID(ctx, bookingID); err == nil && full != nil {
		b = full
	}
	name, phone := s.contact(ctx, b)
	s.scheduleReminder(ctx, b, name, phone)
	s.notifyCustomerRescheduled(ctx, b, oldStart)
	s.notifyGroupSlot(ctx, b, "rescheduled", actorLabel(actorID), oldStart)
	return nil
}

// CorrectDetailsRequest is an admin correction of booking details that CS
// cannot change after creation (service/price, place/address, notes).
type CorrectDetailsRequest struct {
	ServiceID      uuid.UUID
	Place          domain.Place
	VisitAddress   string
	Notes          string
	DistanceZoneID *uuid.UUID // nil = keep the current band unless the place changes
	Reason         string     // mandatory correction justification, logged
	ActorID        uuid.UUID
}

// CorrectDetails applies an admin correction on any booking, logging old→new.
// It returns priceBelowPaid=true when the corrected price drops below what
// was already paid, so the UI can suggest refunding the difference. The
// charged payment row itself is never altered.
func (s *BookingService) CorrectDetails(ctx context.Context, bookingID uuid.UUID, req CorrectDetailsRequest) (priceBelowPaid bool, err error) {
	b, err := s.bookings.ByID(ctx, bookingID)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(req.Reason) == "" {
		return false, fmt.Errorf("%w: correction reason is required", ErrValidation)
	}
	place, visitAddress, err := normalizePlace(req.Place, req.VisitAddress)
	if err != nil {
		return false, err
	}
	serviceID := b.ServiceID
	priceIDR := b.PriceIDR
	var changes []string
	if req.ServiceID != uuid.Nil && req.ServiceID != b.ServiceID {
		svc, err := s.catalog.ServiceByID(ctx, req.ServiceID)
		if err != nil {
			return false, err
		}
		if svc.LocationID != b.LocationID {
			return false, fmt.Errorf("%w: service/location mismatch", ErrValidation)
		}
		changes = append(changes, fmt.Sprintf("service %s → %s (Rp %d → Rp %d)",
			b.ServiceName, svc.Name, b.PriceIDR, svc.PriceIDR))
		serviceID = svc.ID
		priceIDR = svc.PriceIDR
	}
	if place != b.Place || visitAddress != b.VisitAddress {
		changes = append(changes, fmt.Sprintf("place %s → %s", b.Place, place))
	}
	distanceFee, distanceKm := b.DistanceFeeIDR, b.DistanceKm
	switch {
	case place != domain.PlaceHomecare:
		// Onsite never carries transport.
		if distanceFee != 0 || distanceKm != nil {
			changes = append(changes, fmt.Sprintf("transport Rp %d → Rp 0", distanceFee))
		}
		distanceFee, distanceKm = 0, nil
	case req.DistanceZoneID != nil || b.Place != domain.PlaceHomecare:
		fee, km, err := ResolveDistance(ctx, s.zones, place, req.DistanceZoneID)
		if err != nil {
			return false, err
		}
		// Same band: keep the booking's snapshot, so a later edit of the
		// zone table never reprices an unrelated correction.
		if !sameKm(km, b.DistanceKm) {
			if fee != distanceFee || !sameKm(km, distanceKm) {
				changes = append(changes, fmt.Sprintf("transport Rp %d → Rp %d", distanceFee, fee))
			}
			distanceFee, distanceKm = fee, km
		}
	}
	notes := strings.TrimSpace(req.Notes)
	if notes != b.Notes {
		changes = append(changes, "notes updated")
	}
	if len(changes) == 0 {
		return false, fmt.Errorf("%w: nothing to correct", ErrValidation)
	}
	logNotes := strings.TrimSpace(req.Reason) + " [" + strings.Join(changes, "; ") + "]"
	if err := s.bookings.UpdateDetailsWithLog(ctx, bookingID, serviceID, priceIDR,
		place, visitAddress, notes, distanceKm, distanceFee, &req.ActorID, logNotes); err != nil {
		return false, err
	}
	if p, err := s.payments.ByBookingID(ctx, bookingID); err == nil && p != nil &&
		p.Status == domain.PaymentPaid && p.AmountIDR > priceIDR+distanceFee {
		return true, nil
	}
	return false, nil
}

func (s *BookingService) ListFiltered(ctx context.Context, filter repo.FilterParams) ([]domain.Booking, error) {
	return s.bookings.ListFiltered(ctx, filter)
}

// CountFiltered returns the total rows the filter matches, ignoring the page
// window. The portal pager uses it for "Page X of Y".
func (s *BookingService) CountFiltered(ctx context.Context, filter repo.FilterParams) (int, error) {
	return s.bookings.CountFiltered(ctx, filter)
}

func (s *BookingService) ListLogs(ctx context.Context, bookingID uuid.UUID) ([]domain.BookingLog, error) {
	return s.bookings.ListLogs(ctx, bookingID)
}

func (s *BookingService) GetBooking(ctx context.Context, id uuid.UUID) (*domain.Booking, error) {
	return s.bookings.ByID(ctx, id)
}

func (s *BookingService) GetPaymentForBooking(ctx context.Context, bookingID uuid.UUID) (*domain.Payment, error) {
	return s.payments.ByBookingID(ctx, bookingID)
}

func (s *BookingService) ListMine(ctx context.Context, customerID uuid.UUID) ([]domain.Booking, error) {
	return s.bookings.ListByCustomer(ctx, customerID)
}

func (s *BookingService) Cancel(ctx context.Context, bookingID uuid.UUID, actorID *uuid.UUID, isStaff bool, reason string) error {
	b, err := s.bookings.ByID(ctx, bookingID)
	if err != nil {
		return err
	}
	// Guests arrive pre-authorized (nil actor after token check); accounts
	// must own the booking.
	if actorID == nil {
		if !b.IsGuest() {
			return ErrForbidden
		}
	} else if !isStaff && !b.OwnedBy(*actorID) {
		return ErrForbidden
	}
	if b.Status != domain.BookingConfirmed && b.Status != domain.BookingPendingPayment {
		return fmt.Errorf("%w: cannot cancel booking with status %s", ErrValidation, b.Status)
	}
	note := "Cancelled by user/staff"
	if strings.TrimSpace(reason) != "" {
		note += ": " + strings.TrimSpace(reason)
	}
	return s.UpdateStatus(ctx, bookingID, domain.BookingCancelled, actorID, note)
}

func (s *BookingService) HandleMidtransNotification(ctx context.Context, raw []byte) error {
	var n payment.Notification
	if err := json.Unmarshal(raw, &n); err != nil {
		return err
	}
	if !s.midtrans.VerifySignature(n) {
		return fmt.Errorf("%w: bad signature", ErrValidation)
	}
	p, err := s.payments.ByOrderID(ctx, n.OrderID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if p.Status == domain.PaymentPaid {
		return nil
	}
	if payment.IsPaidStatus(n.TransactionStatus, n.FraudStatus) {
		if err := s.payments.MarkPaid(ctx, n.OrderID, n.TransactionID, string(raw)); err != nil {
			return err
		}
		if err := s.UpdateStatus(ctx, p.BookingID, domain.BookingConfirmed, nil, "Payment confirmed via Midtrans QRIS"); err != nil {
			return err
		}
		b, err := s.bookings.ByID(ctx, p.BookingID)
		if err != nil {
			return err
		}
		manage := s.baseURL + "/bookings"
		if b.IsGuest() && b.AccessToken != nil {
			manage = fmt.Sprintf("%s/bookings/%s/pay?t=%s", s.baseURL, b.ID.String(), *b.AccessToken)
		}
		if email, name := s.bookingRecipient(ctx, b); email != "" {
			body := fmt.Sprintf("Hi %s, booking confirmed at Wangga Spa Baby, Mom, & Woman.\n\nService: %s\nLocation: %s\nTherapist: %s\nWhen: %s\nAmount: Rp %d\n\nManage: %s\n",
				name, b.ServiceName, b.LocationName, b.StaffName, b.StartAt.Format(time.RFC1123), b.Net(), manage)
			if err := s.mailer.Send(email, "Wangga booking confirmed", body); err != nil {
				s.log.Warn("email send failed", "err", err)
			}
		}
		return nil
	}
	if payment.IsFailureStatus(n.TransactionStatus) {
		status := domain.PaymentFailed
		if n.TransactionStatus == "expire" {
			status = domain.PaymentExpired
		}
		_ = s.payments.MarkStatus(ctx, n.OrderID, status, string(raw))
		// Only the first failure notification expires the hold: later webhooks
		// would otherwise re-send the customer notice.
		if b0, err := s.bookings.ByID(ctx, p.BookingID); err == nil && b0.Status == domain.BookingPendingPayment {
			_ = s.UpdateStatus(ctx, p.BookingID, domain.BookingExpired, nil, "Payment marked "+string(status)+" by Midtrans")
		}
	}
	return nil
}

func (s *BookingService) ConfirmMockPayment(ctx context.Context, bookingID uuid.UUID, actorID *uuid.UUID) error {
	b, err := s.bookings.ByID(ctx, bookingID)
	if err != nil {
		return err
	}
	// Guests arrive pre-authorized (nil actor after token check).
	if actorID == nil {
		if !b.IsGuest() {
			return ErrForbidden
		}
	} else {
		actor, err := s.users.ByID(ctx, *actorID)
		if err != nil {
			return err
		}
		if !b.OwnedBy(*actorID) && !actor.Role.IsStaff() {
			return ErrForbidden
		}
	}
	p, err := s.payments.ByBookingID(ctx, bookingID)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(payment.Notification{
		OrderID: p.OrderID, TransactionID: p.MidtransTransID,
		TransactionStatus: "settlement", StatusCode: "200", GrossAmount: fmt.Sprintf("%d.00", p.AmountIDR),
	})
	return s.HandleMidtransNotification(ctx, raw)
}

func (s *BookingService) ExpireHolds(ctx context.Context) error {
	// Pending outbox rows for these bookings are cancelled by the same
	// statement; the messages below are what the customers still need to hear.
	ids, err := s.bookings.ExpireHolds(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		b, err := s.bookings.ByID(ctx, id)
		if err != nil {
			s.log.Warn("wa expire lookup", "booking", id, "err", err)
			continue
		}
		s.notifyCustomerExpired(ctx, b)
		s.notifyGroupSlot(ctx, b, "expired", "sistem", time.Time{})
	}
	return nil
}

func isExclusionViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23P01"
	}
	return false
}

// NewAccessToken mints an unguessable guest management token.
func NewAccessToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// ValidateGuest checks guest identity input. Error strings stay English.
func ValidateGuest(name, phone, email string) (string, string, string, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 2 || n > 80 {
		return "", "", "", fmt.Errorf("%w: name must be 2-80 characters", ErrValidation)
	}
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phone)
	if len(digits) < 9 || len(digits) > 15 {
		return "", "", "", fmt.Errorf("%w: enter a valid phone number", ErrValidation)
	}
	email = strings.TrimSpace(email)
	if email != "" {
		if _, err := mail.ParseAddress(email); err != nil {
			return "", "", "", fmt.Errorf("%w: enter a valid email address", ErrValidation)
		}
	}
	return name, digits, email, nil
}

// GuestBookingRequest carries guest identity for account-less bookings.
type GuestBookingRequest struct {
	LocationID     uuid.UUID
	ServiceID      uuid.UUID
	StaffID        uuid.UUID
	StartAt        time.Time
	Notes          string
	Place          domain.Place
	VisitAddress   string
	PromoCode      string
	DistanceZoneID *uuid.UUID
	Name           string
	Phone          string
	Email          string
}

// CreateGuestPending books without an account: identity snapshots onto the
// row and a management token grants pay/cancel access.
func (s *BookingService) CreateGuestPending(ctx context.Context, req GuestBookingRequest) (*domain.Booking, *domain.Payment, error) {
	name, phone, email, err := ValidateGuest(req.Name, req.Phone, req.Email)
	if err != nil {
		return nil, nil, err
	}
	svc, err := s.catalog.ServiceByID(ctx, req.ServiceID)
	if err != nil {
		return nil, nil, err
	}
	if svc.LocationID != req.LocationID {
		return nil, nil, fmt.Errorf("%w: service/location mismatch", ErrValidation)
	}
	place, visitAddress, err := normalizePlace(req.Place, req.VisitAddress)
	if err != nil {
		return nil, nil, err
	}
	st, err := s.catalog.StaffByID(ctx, req.StaffID)
	if err != nil {
		return nil, nil, err
	}
	if st.LocationID != req.LocationID {
		return nil, nil, fmt.Errorf("%w: staff/location mismatch", ErrValidation)
	}
	quote, err := QuotePromo(ctx, s.promos, req.PromoCode, svc.PriceIDR)
	if err != nil {
		return nil, nil, err
	}
	distanceFee, distanceKm, err := ResolveDistance(ctx, s.zones, place, req.DistanceZoneID)
	if err != nil {
		return nil, nil, err
	}
	token, err := NewAccessToken()
	if err != nil {
		return nil, nil, err
	}
	endAt := req.StartAt.Add(time.Duration(svc.DurationMin) * time.Minute)
	holdExp := time.Now().Add(s.holdTTL)
	b := &domain.Booking{
		LocationID:     req.LocationID,
		ServiceID:      req.ServiceID,
		StaffID:        req.StaffID,
		StartAt:        req.StartAt.UTC(),
		EndAt:          endAt.UTC(),
		Status:         domain.BookingPendingPayment,
		PriceIDR:       svc.PriceIDR,
		PromoCode:      quote.Code,
		DiscountIDR:    quote.Discount,
		GuestName:      name,
		GuestPhone:     phone,
		GuestEmail:     email,
		AccessToken:    &token,
		HoldExpiresAt:  &holdExp,
		Notes:          strings.TrimSpace(req.Notes),
		Place:          place,
		VisitAddress:   visitAddress,
		DistanceKm:     distanceKm,
		DistanceFeeIDR: distanceFee,
		Source:         "web",
	}
	if s.ManualPayments() {
		b.Status = domain.BookingConfirmed
		b.HoldExpiresAt = nil
	}
	if err := s.bookings.Create(ctx, b); err != nil {
		if isExclusionViolation(err) {
			return nil, nil, ErrConflict
		}
		return nil, nil, err
	}
	redeemed := false
	if quote.Code != "" {
		if err := s.promos.TryRedeem(ctx, quote.Code); err != nil {
			_ = s.bookings.UpdateStatus(ctx, b.ID, domain.BookingExpired)
			return nil, nil, fmt.Errorf("%w: promo code just reached its limit", ErrValidation)
		}
		redeemed = true
	}
	orderID := orderRef(b.ID)
	var p *domain.Payment
	if s.ManualPayments() {
		p = &domain.Payment{
			BookingID:      b.ID,
			OrderID:        orderID,
			AmountIDR:      b.Net(),
			Status:         domain.PaymentPending,
			PaymentMethod:  domain.PaymentMethodQRISStatic,
			PaymentPurpose: domain.PaymentPurposeFull,
		}
		if err := s.payments.Create(ctx, p); err != nil {
			return nil, nil, err
		}
		_ = s.bookings.LogActivity(ctx, b.ID, nil, "Guest booking created online (manual QRIS pending)", req.Notes)
		logCreationStatus(ctx, s.bookings, b, nil)
	} else {
		charge, err := s.midtrans.ChargeQRIS(ctx, payment.QRISChargeRequest{
			OrderID:      orderID,
			AmountIDR:    b.Net(),
			CustomerName: name,
			Email:        email,
			ItemName:     svc.Name,
		})
		if err != nil {
			if redeemed {
				_ = s.promos.Unredeem(ctx, quote.Code)
			}
			_ = s.bookings.UpdateStatus(ctx, b.ID, domain.BookingExpired)
			return nil, nil, fmt.Errorf("qris charge: %w", err)
		}
		p = &domain.Payment{
			BookingID:       b.ID,
			OrderID:         orderID,
			AmountIDR:       b.Net(),
			Status:          domain.PaymentPending,
			PaymentMethod:   domain.PaymentMethodQRIS,
			PaymentPurpose:  domain.PaymentPurposeFull,
			QRString:        charge.QRString,
			MidtransTransID: charge.TransactionID,
		}
		if err := s.payments.Create(ctx, p); err != nil {
			return nil, nil, err
		}
		_ = s.bookings.LogActivity(ctx, b.ID, nil, "Guest booking created online (QRIS pending)", req.Notes)
		logCreationStatus(ctx, s.bookings, b, nil)
	}
	full, _ := s.bookings.ByID(ctx, b.ID)
	if full != nil {
		b = full
	}
	if s.ManualPayments() {
		if email != "" {
			_ = s.mailer.Send(email, "Wangga booking confirmed",
				fmt.Sprintf("Thanks %s! Your Wangga booking for %s is confirmed.\n\nWhen: %s\nAmount: Rp %d\n\nPay online: %s/bookings/%s/pay?t=%s\nOr pay at the counter when you arrive.\n",
					name, svc.Name, formatWIB(b.StartAt), b.Net(), s.baseURL, b.ID.String(), token))
		}
		s.onConfirmed(ctx, b, p, name, phone)
	} else if email != "" {
		_ = s.mailer.Send(email, "Wangga booking received",
			fmt.Sprintf("Thanks %s! Your Wangga booking for %s is pending payment of Rp %d.\n\nManage: %s/bookings/%s/pay?t=%s\n",
				name, svc.Name, b.Net(), s.baseURL, b.ID.String(), token))
	}
	return b, p, nil
}
