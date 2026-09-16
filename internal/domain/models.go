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
	IsActive     bool
	BabyName     string
	BabyDOB      *time.Time
	MomCondition string
	MedicalNotes string
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
	ImagePath   string // local upload under uploads/services/
	ImageURL    string // internet picture link
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type ServiceHours struct {
	ID        uuid.UUID
	ServiceID uuid.UUID
	Weekday   int    // 0=Sunday .. 6=Saturday
	StartTime string // HH:MM
	EndTime   string // HH:MM
}

type EmploymentType string

const (
	EmploymentFullTime  EmploymentType = "full_time"
	EmploymentPartTime  EmploymentType = "part_time"
	EmploymentContract  EmploymentType = "contract"
	EmploymentFreelance EmploymentType = "freelance"
)

func (e EmploymentType) Display() string {
	switch e {
	case EmploymentFullTime:
		return "Full-time"
	case EmploymentPartTime:
		return "Part-time"
	case EmploymentContract:
		return "Contract"
	case EmploymentFreelance:
		return "Freelance"
	default:
		return string(e)
	}
}

type Job struct {
	ID             uuid.UUID
	Title          string
	LocationID     *uuid.UUID
	LocationName   string
	EmploymentType EmploymentType
	Description    string
	Requirements   string
	Active         bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type JobApplication struct {
	ID             uuid.UUID
	JobID          uuid.UUID
	JobTitle       string
	FullName       string
	Email          string
	Phone          string
	Message        string
	CVFilename     string
	CVOriginalName string
	CreatedAt      time.Time
}

type Staff struct {
	ID         uuid.UUID
	LocationID uuid.UUID
	Name       string
	Bio        string
	Active     bool
	PhotoPath  string // local upload under uploads/staff/
	PhotoURL   string // internet picture link
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type StaffHours struct {
	ID        uuid.UUID
	StaffID   uuid.UUID
	Weekday   int    // 0=Sunday .. 6=Saturday
	StartTime string // HH:MM
	EndTime   string // HH:MM
}

type StaffTimeOff struct {
	ID        uuid.UUID
	StaffID   uuid.UUID
	StaffName string
	StartAt   time.Time
	EndAt     time.Time
	Reason    string
	CreatedAt time.Time
}

type StaffService struct {
	StaffID   uuid.UUID
	ServiceID uuid.UUID
}

type BookingStatus string

const (
	BookingPendingPayment BookingStatus = "pending_payment"
	BookingConfirmed      BookingStatus = "confirmed"
	BookingCheckedIn      BookingStatus = "checked_in"
	BookingInService      BookingStatus = "in_service"
	BookingCompleted      BookingStatus = "completed"
	BookingCancelled      BookingStatus = "cancelled"
	BookingExpired        BookingStatus = "expired"
	BookingNoShow         BookingStatus = "no_show"
)

func (s BookingStatus) Display() string {
	switch s {
	case BookingPendingPayment:
		return "Pending Payment"
	case BookingConfirmed:
		return "Confirmed"
	case BookingCheckedIn:
		return "Checked In"
	case BookingInService:
		return "In Service"
	case BookingCompleted:
		return "Completed"
	case BookingCancelled:
		return "Cancelled"
	case BookingExpired:
		return "Expired"
	case BookingNoShow:
		return "No Show"
	default:
		return string(s)
	}
}

type PaymentMethod string

const (
	PaymentMethodQRIS PaymentMethod = "qris"
	PaymentMethodCash PaymentMethod = "cash"
)

type PaymentPurpose string

const (
	PaymentPurposeBookingFee PaymentPurpose = "booking_fee"
	PaymentPurposeService    PaymentPurpose = "service"
	PaymentPurposeFull       PaymentPurpose = "full"
)

// Place is where a booking is fulfilled: at the branch or at the customer's home.
type Place string

const (
	PlaceOnsite   Place = "onsite"
	PlaceHomecare Place = "homecare"
)

type Booking struct {
	ID            uuid.UUID
	CustomerID    uuid.UUID
	LocationID    uuid.UUID
	ServiceID     uuid.UUID
	StaffID       uuid.UUID
	StartAt       time.Time
	EndAt         time.Time
	Status        BookingStatus
	PriceIDR      int64
	BookingFeeIDR int64
	HoldExpiresAt *time.Time
	Notes         string
	Place         Place  // 'onsite' (at branch) or 'homecare' (customer address)
	VisitAddress  string // required when Place is homecare
	Source        string // 'web', 'walkin', 'whatsapp', 'phone'
	CreatedByID   *uuid.UUID
	UpdatedByID   *uuid.UUID
	CreatedAt     time.Time
	UpdatedAt     time.Time
	CustomerName  string
	CustomerPhone string
	CustomerEmail string
	ServiceName   string
	StaffName     string
	LocationName  string
	BabyName      string
	MomCondition  string
	PaymentMethod PaymentMethod
	PaymentStatus PaymentStatus
}

type BookingLog struct {
	ID        uuid.UUID
	BookingID uuid.UUID
	UserID    *uuid.UUID
	UserName  string
	Action    string
	Notes     string
	CreatedAt time.Time
}

type PaymentStatus string

const (
	PaymentPending PaymentStatus = "pending"
	PaymentPaid    PaymentStatus = "paid"
	PaymentFailed  PaymentStatus = "failed"
	PaymentExpired PaymentStatus = "expired"
)

type Payment struct {
	ID              uuid.UUID
	BookingID       uuid.UUID
	OrderID         string
	AmountIDR       int64
	Status          PaymentStatus
	PaymentMethod   PaymentMethod
	PaymentPurpose  PaymentPurpose
	QRString        string
	MidtransTransID string
	RawNotification string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	PaidAt          *time.Time
}

type Session struct {
	ID        string
	UserID    uuid.UUID
	ExpiresAt time.Time
	CreatedAt time.Time
}

// Refund records money returned to a customer (cash basis: it deducts
// from the month it was recorded in, not the original payment month).
type Refund struct {
	ID          uuid.UUID
	BookingID   uuid.UUID
	AmountIDR   int64
	Reason      string
	CreatedByID *uuid.UUID
	CreatedBy   string
	CreatedAt   time.Time
}

// ReportLine is one paid payment row in a monthly revenue report.
type ReportLine struct {
	PaymentID     uuid.UUID
	BookingID     uuid.UUID
	AmountIDR     int64
	PaymentMethod PaymentMethod
	Purpose       PaymentPurpose
	PaidAt        time.Time
	StartAt       time.Time
	CustomerID    uuid.UUID
	CustomerName  string
	ServiceID     uuid.UUID
	ServiceName   string
	StaffID       uuid.UUID
	StaffName     string
	LocationName  string
	LocationID    uuid.UUID
	Status        BookingStatus
}

// ReportRefund is one recorded refund row in a monthly revenue report.
type ReportRefund struct {
	Refund
	CustomerName string
	ServiceName  string
	StaffID      uuid.UUID
	StaffName    string
	LocationName string
	LocationID   uuid.UUID
}

// ReportSummary aggregates a month: Net = Gross - Refunds.
type ReportSummary struct {
	Month       string // YYYY-MM
	GrossIDR    int64
	RefundIDR   int64
	NetIDR      int64
	Count       int
	RefundCnt   int
	QRISIDR     int64
	CashIDR     int64
	ServiceRows []NameTotal // sorted desc
	StaffRows   []StaffRow  // sorted desc by net
}

// NameTotal is a named amount row (service breakdowns).
type NameTotal struct {
	Name  string
	Total int64
}

// StaffServiceRow counts one service handled by one staff member.
type StaffServiceRow struct {
	Name  string
	Count int
	Total int64
}

// StaffRow is the per-bidan/staff detail: workload, reach, mix, ticket.
type StaffRow struct {
	StaffID         uuid.UUID
	Name            string
	Sessions        int
	UniqueCustomers int
	GrossIDR        int64
	RefundIDR       int64
	NetIDR          int64
	AvgIDR          int64
	TopService      string
	Services        []StaffServiceRow // sorted desc by count, then total
	TopServices     []StaffServiceRow // first 3 of Services, for compact display
	MoreCount       int               // len(Services) - len(TopServices)
}

type Slot struct {
	StartAt time.Time
	EndAt   time.Time
	StaffID uuid.UUID
}
