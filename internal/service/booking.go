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
	bookings   *repo.BookingRepo
	payments   *repo.PaymentRepo
	catalog    *repo.CatalogRepo
	users      *repo.UserRepo
	avail      *AvailabilityService
	midtrans   *payment.Client
	mailer     *email.Sender
	holdTTL    time.Duration
	baseURL    string
	log        *slog.Logger
}

func NewBookingService(
	bookings *repo.BookingRepo,
	payments *repo.PaymentRepo,
	catalog *repo.CatalogRepo,
	users *repo.UserRepo,
	avail *AvailabilityService,
	midtrans *payment.Client,
	mailer *email.Sender,
	holdTTL time.Duration,
	baseURL string,
	log *slog.Logger,
) *BookingService {
	return &BookingService{
		bookings: bookings, payments: payments, catalog: catalog, users: users,
		avail: avail, midtrans: midtrans, mailer: mailer, holdTTL: holdTTL, baseURL: baseURL, log: log,
	}
}

func (s *BookingService) CreatePending(ctx context.Context, customerID, locationID, serviceID, staffID uuid.UUID, startAt time.Time, notes string) (*domain.Booking, *domain.Payment, error) {
	svc, err := s.catalog.ServiceByID(ctx, serviceID)
	if err != nil {
		return nil, nil, err
	}
	if svc.LocationID != locationID {
		return nil, nil, fmt.Errorf("%w: service/location mismatch", ErrValidation)
	}
	st, err := s.catalog.StaffByID(ctx, staffID)
	if err != nil {
		return nil, nil, err
	}
	if st.LocationID != locationID {
		return nil, nil, fmt.Errorf("%w: staff/location mismatch", ErrValidation)
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
		HoldExpiresAt: &holdExp,
		Notes:         strings.TrimSpace(notes),
	}
	if err := s.bookings.Create(ctx, b); err != nil {
		if isExclusionViolation(err) {
			return nil, nil, ErrConflict
		}
		return nil, nil, err
	}
	customer, err := s.users.ByID(ctx, customerID)
	if err != nil {
		return nil, nil, err
	}
	orderID := fmt.Sprintf("SPA-%s", strings.ReplaceAll(b.ID.String(), "-", "")[:16])
	charge, err := s.midtrans.ChargeQRIS(ctx, payment.QRISChargeRequest{
		OrderID:      orderID,
		AmountIDR:    b.PriceIDR,
		CustomerName: customer.FullName,
		Email:        customer.Email,
		ItemName:     svc.Name,
	})
	if err != nil {
		_ = s.bookings.UpdateStatus(ctx, b.ID, domain.BookingExpired)
		return nil, nil, fmt.Errorf("qris charge: %w", err)
	}
	p := &domain.Payment{
		BookingID:       b.ID,
		OrderID:         orderID,
		AmountIDR:       b.PriceIDR,
		Status:          domain.PaymentPending,
		QRString:        charge.QRString,
		MidtransTransID: charge.TransactionID,
	}
	if err := s.payments.Create(ctx, p); err != nil {
		return nil, nil, err
	}
	full, _ := s.bookings.ByID(ctx, b.ID)
	if full != nil {
		b = full
	}
	return b, p, nil
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

func (s *BookingService) Cancel(ctx context.Context, bookingID, actorID uuid.UUID, isStaff bool) error {
	b, err := s.bookings.ByID(ctx, bookingID)
	if err != nil {
		return err
	}
	if !isStaff && b.CustomerID != actorID {
		return ErrForbidden
	}
	if b.Status != domain.BookingConfirmed && b.Status != domain.BookingPendingPayment {
		return fmt.Errorf("%w: cannot cancel", ErrValidation)
	}
	if err := s.bookings.UpdateStatus(ctx, bookingID, domain.BookingCancelled); err != nil {
		return err
	}
	customer, err := s.users.ByID(ctx, b.CustomerID)
	if err == nil {
		_ = s.mailer.Send(customer.Email, "Wangga booking cancelled",
			fmt.Sprintf("Your Wangga booking for %s on %s was cancelled.", b.ServiceName, b.StartAt.Format(time.RFC1123)))
	}
	return nil
}

// CreateForCustomer lets CS/admin book on behalf of a customer.
// If markConfirmed, booking is confirmed immediately (counter/cash); otherwise QRIS pending.
func (s *BookingService) CreateForCustomer(ctx context.Context, customerID, locationID, serviceID, staffID uuid.UUID, startAt time.Time, notes string, markConfirmed bool) (*domain.Booking, *domain.Payment, error) {
	if markConfirmed {
		svc, err := s.catalog.ServiceByID(ctx, serviceID)
		if err != nil {
			return nil, nil, err
		}
		if svc.LocationID != locationID {
			return nil, nil, fmt.Errorf("%w: service/location mismatch", ErrValidation)
		}
		st, err := s.catalog.StaffByID(ctx, staffID)
		if err != nil {
			return nil, nil, err
		}
		if st.LocationID != locationID {
			return nil, nil, fmt.Errorf("%w: staff/location mismatch", ErrValidation)
		}
		endAt := startAt.Add(time.Duration(svc.DurationMin) * time.Minute)
		b := &domain.Booking{
			CustomerID: customerID, LocationID: locationID, ServiceID: serviceID, StaffID: staffID,
			StartAt: startAt.UTC(), EndAt: endAt.UTC(), Status: domain.BookingConfirmed,
			PriceIDR: svc.PriceIDR, Notes: strings.TrimSpace(notes),
		}
		if err := s.bookings.Create(ctx, b); err != nil {
			if isExclusionViolation(err) {
				return nil, nil, ErrConflict
			}
			return nil, nil, err
		}
		full, _ := s.bookings.ByID(ctx, b.ID)
		if full != nil {
			b = full
		}
		customer, err := s.users.ByID(ctx, customerID)
		if err == nil {
			_ = s.mailer.Send(customer.Email, "Wangga booking confirmed",
				fmt.Sprintf("Your Wangga booking for %s on %s is confirmed.\nTherapist: %s\nLocation: %s\n",
					b.ServiceName, b.StartAt.Format(time.RFC1123), b.StaffName, b.LocationName))
		}
		return b, nil, nil
	}
	return s.CreatePending(ctx, customerID, locationID, serviceID, staffID, startAt, notes)
}

func (s *BookingService) UpdateBooking(ctx context.Context, bookingID, staffID uuid.UUID, startAt time.Time, status domain.BookingStatus, notes string) (*domain.Booking, error) {
	b, err := s.bookings.ByID(ctx, bookingID)
	if err != nil {
		return nil, err
	}
	svc, err := s.catalog.ServiceByID(ctx, b.ServiceID)
	if err != nil {
		return nil, err
	}
	if staffID == uuid.Nil {
		staffID = b.StaffID
	}
	endAt := startAt.Add(time.Duration(svc.DurationMin) * time.Minute)
	if status == "" {
		status = b.Status
	}
	switch status {
	case domain.BookingPendingPayment, domain.BookingConfirmed, domain.BookingCancelled, domain.BookingExpired:
	default:
		return nil, fmt.Errorf("%w: invalid status", ErrValidation)
	}
	if err := s.bookings.UpdateDetails(ctx, bookingID, staffID, startAt.UTC(), endAt.UTC(), status, notes); err != nil {
		if isExclusionViolation(err) {
			return nil, ErrConflict
		}
		return nil, err
	}
	return s.bookings.ByID(ctx, bookingID)
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
		b, err := s.bookings.ByID(ctx, p.BookingID)
		if err != nil {
			return err
		}
		customer, err := s.users.ByID(ctx, b.CustomerID)
		if err == nil {
			body := fmt.Sprintf("Booking confirmed at Wangga: Baby & Mom Spa.\n\nService: %s\nLocation: %s\nTherapist: %s\nWhen: %s\nAmount: Rp %d\n\nManage: %s/bookings\n",
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
