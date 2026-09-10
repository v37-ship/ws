package domain

import (
	"time"

	"github.com/google/uuid"
)

type Role string

const (
	RoleCustomer Role = "customer"
	RoleAdmin    Role = "admin"
	RoleCS       Role = "cs"
)

func (r Role) IsStaff() bool {
	return r == RoleAdmin || r == RoleCS
}

type User struct {
	ID           uuid.UUID
	Email        string
	PasswordHash string
	FullName     string
	Phone        string
	Role         Role
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type Location struct {
	ID        uuid.UUID
	Name      string
	Address   string
	City      string
	Timezone  string
	Active    bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Service struct {
	ID          uuid.UUID
	LocationID  uuid.UUID
	Name        string
	Description string
	DurationMin int
	PriceIDR    int64
	Active      bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Staff struct {
	ID         uuid.UUID
	LocationID uuid.UUID
	Name       string
	Bio        string
	Active     bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type StaffHours struct {
	ID        uuid.UUID
	StaffID   uuid.UUID
	Weekday   int // 0=Sunday .. 6=Saturday
	StartTime string // HH:MM
	EndTime   string // HH:MM
}

type StaffService struct {
	StaffID   uuid.UUID
	ServiceID uuid.UUID
}

type BookingStatus string

const (
	BookingPendingPayment BookingStatus = "pending_payment"
	BookingConfirmed      BookingStatus = "confirmed"
	BookingCancelled      BookingStatus = "cancelled"
	BookingExpired        BookingStatus = "expired"
)

type Booking struct {
	ID              uuid.UUID
	CustomerID      uuid.UUID
	LocationID      uuid.UUID
	ServiceID       uuid.UUID
	StaffID         uuid.UUID
	StartAt         time.Time
	EndAt           time.Time
	Status          BookingStatus
	PriceIDR        int64
	HoldExpiresAt   *time.Time
	Notes           string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	CustomerName    string
	ServiceName     string
	StaffName       string
	LocationName    string
}

type PaymentStatus string

const (
	PaymentPending PaymentStatus = "pending"
	PaymentPaid    PaymentStatus = "paid"
	PaymentFailed  PaymentStatus = "failed"
	PaymentExpired PaymentStatus = "expired"
)

type Payment struct {
	ID                uuid.UUID
	BookingID         uuid.UUID
	OrderID           string
	AmountIDR         int64
	Status            PaymentStatus
	QRString          string
	MidtransTransID   string
	RawNotification   string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	PaidAt            *time.Time
}

type Session struct {
	ID        string
	UserID    uuid.UUID
	ExpiresAt time.Time
	CreatedAt time.Time
}

type Slot struct {
	StartAt time.Time
	EndAt   time.Time
	StaffID uuid.UUID
}
