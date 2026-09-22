package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

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
	avail    *AvailabilityService
	midtrans *payment.Client
	mailer   *email.Sender
	holdTTL  time.Duration
	baseURL  string
	log      *slog.Logger
}

func NewBookingService(
	bookings *repo.BookingRepo,
	payments *repo.PaymentRepo,
	catalog *repo.CatalogRepo,
	users *repo.UserRepo,
	promos *repo.PromoRepo,
	avail *AvailabilityService,
	midtrans *payment.Client,
	mailer *email.Sender,
	holdTTL time.Duration,
	baseURL string,
	log *slog.Logger,
) *BookingService {
	return &BookingService{
		bookings: bookings, payments: payments, catalog: catalog, users: users, promos: promos,
		avail: avail, midtrans: midtrans, mailer: mailer, holdTTL: holdTTL, baseURL: baseURL, log: log,
	}
}

func (s *BookingService) CreatePending(ctx context.Context, customerID, locationID, serviceID, staffID uuid.UUID, startAt time.Time, notes string, place domain.Place, visitAddress string, promoCode string) (*domain.Booking, *domain.Payment, error) {
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
	endAt := startAt.Add(time.Duration(svc.DurationMin) * time.Minute)
	holdExp := time.Now().Add(s.holdTTL)
	b := &domain.Booking{
		CustomerID:    customerID,
		LocationID:    locationID,
		ServiceID:     serviceID,
		StaffID:       staffID,
		StartAt:       startAt.UTC(),
		EndAt:         endAt.UTC(),
		Status:        domain.BookingPendingPayment,
		PriceIDR:      svc.PriceIDR,
		PromoCode:     quote.Code,
		DiscountIDR:   quote.Discount,
		HoldExpiresAt: &holdExp,
		Notes:         strings.TrimSpace(notes),
		Place:         place,
		VisitAddress:  visitAddress,
		Source:        "web",
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
	orderID := fmt.Sprintf("SPA-%s", strings.ReplaceAll(b.ID.String(), "-", "")[:16])
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
	p := &domain.Payment{
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
	full, _ := s.bookings.ByID(ctx, b.ID)
	if full != nil {
		b = full
	}
	return b, p, nil
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
	b := &domain.Booking{
		CustomerID:   req.CustomerID,
		LocationID:   req.LocationID,
		ServiceID:    req.ServiceID,
		StaffID:      req.StaffID,
		StartAt:      req.StartAt.UTC(),
		EndAt:        endAt.UTC(),
		PriceIDR:     svc.PriceIDR,
		PromoCode:    quote.Code,
		DiscountIDR:  quote.Discount,
		Notes:        strings.TrimSpace(req.Notes),
		Place:        place,
		VisitAddress: visitAddress,
		Source:       req.Source,
		CreatedByID:  &req.ActorID,
		UpdatedByID:  &req.ActorID,
	}

	orderID := fmt.Sprintf("STAFF-%s", strings.ReplaceAll(uuid.New().String(), "-", "")[:14])
	var p *domain.Payment

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
	}

	full, _ := s.bookings.ByID(ctx, b.ID)
	if full != nil {
		b = full
	}
	return b, p, nil
}

func (s *BookingService) UpdateStatus(ctx context.Context, bookingID uuid.UUID, newStatus domain.BookingStatus, actorID *uuid.UUID, note string) error {
	b, err := s.bookings.ByID(ctx, bookingID)
	if err != nil {
		return err
	}
	switch newStatus {
	case domain.BookingPendingPayment, domain.BookingConfirmed, domain.BookingCheckedIn, domain.BookingInService,
		domain.BookingCompleted, domain.BookingCancelled, domain.BookingExpired, domain.BookingNoShow:
	default:
		return fmt.Errorf("%w: unknown booking status %s", ErrValidation, newStatus)
	}

	if err := s.bookings.UpdateStatusWithLog(ctx, bookingID, newStatus, actorID, note); err != nil {
		return err
	}

	if newStatus == domain.BookingCancelled {
		customer, err := s.users.ByID(ctx, b.CustomerID)
		if err == nil {
			_ = s.mailer.Send(customer.Email, "Wangga booking cancelled",
				fmt.Sprintf("Your Wangga booking for %s on %s was cancelled. Note: %s", b.ServiceName, b.StartAt.Format(time.RFC1123), note))
		}
	}
	return nil
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

	customer, err := s.users.ByID(ctx, b.CustomerID)
	if err == nil {
		_ = s.mailer.Send(customer.Email, "Wangga booking rescheduled",
			fmt.Sprintf("Your Wangga booking for %s has been rescheduled to %s. Note: %s",
				b.ServiceName, startAt.Format(time.RFC1123), note))
	}
	return nil
}

// CorrectDetailsRequest is an admin correction of booking details that CS
// cannot change after creation (service/price, place/address, notes).
type CorrectDetailsRequest struct {
	ServiceID    uuid.UUID
	Place        domain.Place
	VisitAddress string
	Notes        string
	Reason       string // mandatory correction justification, logged
	ActorID      uuid.UUID
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
	notes := strings.TrimSpace(req.Notes)
	if notes != b.Notes {
		changes = append(changes, "notes updated")
	}
	if len(changes) == 0 {
		return false, fmt.Errorf("%w: nothing to correct", ErrValidation)
	}
	logNotes := strings.TrimSpace(req.Reason) + " [" + strings.Join(changes, "; ") + "]"
	if err := s.bookings.UpdateDetailsWithLog(ctx, bookingID, serviceID, priceIDR,
		place, visitAddress, notes, &req.ActorID, logNotes); err != nil {
		return false, err
	}
	if p, err := s.payments.ByBookingID(ctx, bookingID); err == nil && p != nil &&
		p.Status == domain.PaymentPaid && p.AmountIDR > priceIDR {
		return true, nil
	}
	return false, nil
}

func (s *BookingService) ListFiltered(ctx context.Context, filter repo.FilterParams) ([]domain.Booking, error) {
	return s.bookings.ListFiltered(ctx, filter)
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

func (s *BookingService) ListAdmin(ctx context.Context) ([]domain.Booking, error) {
	return s.bookings.ListUpcoming(ctx, 100)
}

func (s *BookingService) Cancel(ctx context.Context, bookingID, actorID uuid.UUID, isStaff bool, reason string) error {
	b, err := s.bookings.ByID(ctx, bookingID)
	if err != nil {
		return err
	}
	if !isStaff && b.CustomerID != actorID {
		return ErrForbidden
	}
	if b.Status != domain.BookingConfirmed && b.Status != domain.BookingPendingPayment {
		return fmt.Errorf("%w: cannot cancel booking with status %s", ErrValidation, b.Status)
	}
	note := "Cancelled by user/staff"
	if strings.TrimSpace(reason) != "" {
		note += ": " + strings.TrimSpace(reason)
	}
	return s.UpdateStatus(ctx, bookingID, domain.BookingCancelled, &actorID, note)
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
		if err := s.bookings.UpdateStatus(ctx, p.BookingID, domain.BookingConfirmed); err != nil {
			return err
		}
		_ = s.bookings.LogActivity(ctx, p.BookingID, nil, "Payment confirmed via Midtrans QRIS", "")
		b, err := s.bookings.ByID(ctx, p.BookingID)
		if err != nil {
			return err
		}
		customer, err := s.users.ByID(ctx, b.CustomerID)
		if err == nil {
			body := fmt.Sprintf("Booking confirmed at Wangga Spa Baby, Mom, & Woman.\n\nService: %s\nLocation: %s\nTherapist: %s\nWhen: %s\nAmount: Rp %d\n\nManage: %s/bookings\n",
				b.ServiceName, b.LocationName, b.StaffName, b.StartAt.Format(time.RFC1123), b.PriceIDR, s.baseURL)
			if err := s.mailer.Send(customer.Email, "Wangga booking confirmed", body); err != nil {
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
		_ = s.bookings.UpdateStatus(ctx, p.BookingID, domain.BookingExpired)
		_ = s.bookings.LogActivity(ctx, p.BookingID, nil, "Payment marked "+string(status)+" by Midtrans", "")
	}
	return nil
}

func (s *BookingService) ConfirmMockPayment(ctx context.Context, bookingID uuid.UUID, actorID uuid.UUID) error {
	b, err := s.bookings.ByID(ctx, bookingID)
	if err != nil {
		return err
	}
	actor, err := s.users.ByID(ctx, actorID)
	if err != nil {
		return err
	}
	if b.CustomerID != actorID && !actor.Role.IsStaff() {
		return ErrForbidden
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
	_, err := s.bookings.ExpireHolds(ctx)
	return err
}

func isExclusionViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23P01"
	}
	return false
}
