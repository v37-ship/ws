package domain

import (
	"strings"
	"time"
	"unicode"

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
	PhotoURL     string // profile picture link (upload in PhotoPath or URL)
	PhotoPath    string // local upload under uploads/avatars/
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
	ID            uuid.UUID
	LocationID    uuid.UUID
	Name          string
	NameID        string // Indonesian name; empty falls back to Name
	Description   string
	DescriptionID string // Indonesian description; empty falls back to Description
	DurationMin   int
	PriceIDR      int64
	Active        bool
	ImagePath     string // local upload under uploads/services/
	ImageURL      string // internet picture link
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// DisplayName returns the Indonesian name for lang "id" when set,
// otherwise the canonical name.
func (s Service) DisplayName(lang string) string {
	if lang == "id" && s.NameID != "" {
		return s.NameID
	}
	return s.Name
}

// DisplayDescription returns the Indonesian description for lang "id"
// when set, otherwise the canonical description.
func (s Service) DisplayDescription(lang string) string {
	if lang == "id" && s.DescriptionID != "" {
		return s.DescriptionID
	}
	return s.Description
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
	PaymentMethodQRIS       PaymentMethod = "qris"
	PaymentMethodQRISStatic PaymentMethod = "qris_static"
	PaymentMethodCash       PaymentMethod = "cash"
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
	CustomerID    *uuid.UUID // nil = guest booking (identity in Guest* fields)
	GuestName     string
	GuestPhone    string
	GuestEmail    string
	AccessToken   *string // guest management token, nil = none/revoked
	LocationID    uuid.UUID
	ServiceID     uuid.UUID
	StaffID       uuid.UUID
	StartAt       time.Time
	EndAt         time.Time
	Status        BookingStatus
	PriceIDR      int64
	BookingFeeIDR int64
	PromoCode     string // applied promo code snapshot ('' = none)
	DiscountIDR   int64  // discount snapshot; net charged = PriceIDR - DiscountIDR
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
	ID                 uuid.UUID
	BookingID          uuid.UUID
	OrderID            string
	AmountIDR          int64
	Status             PaymentStatus
	PaymentMethod      PaymentMethod
	PaymentPurpose     PaymentPurpose
	QRString           string
	MidtransTransID    string
	RawNotification    string
	Reference          string
	ReviewNote         string
	ReceiptPath        string
	CustomerReportedAt *time.Time
	VerifiedAt         *time.Time
	VerifiedBy         *uuid.UUID
	CreatedAt          time.Time
	UpdatedAt          time.Time
	PaidAt             *time.Time
}

// PaymentSettings holds the single-row admin configuration for manual QRIS.
type PaymentSettings struct {
	QRISImagePath string
	UpdatedBy     *uuid.UUID
	UpdatedAt     time.Time
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
	VoidedAt    *time.Time
	VoidedByID  *uuid.UUID
	VoidedBy    string
	VoidReason  string
}

// IsVoid reports whether the refund was voided by an admin correction.
func (r Refund) IsVoid() bool { return r.VoidedAt != nil }

// ReportLine is one paid payment row in a monthly revenue report.
type ReportLine struct {
	PaymentID     uuid.UUID
	BookingID     uuid.UUID
	AmountIDR     int64
	PaymentMethod PaymentMethod
	Purpose       PaymentPurpose
	PaidAt        time.Time
	StartAt       time.Time
	CustomerID    *uuid.UUID // nil = guest booking (skipped in unique counts)
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
	Lines           []ReportLine      // paid transactions, sorted by paid date
	Refunds         []ReportRefund    // refunds recorded in the month, by record date
}

type Slot struct {
	StartAt time.Time
	EndAt   time.Time
	StaffID uuid.UUID
}

// TestimonialStatus gates homepage visibility: only approved rows render.
// New customer submissions start pending; admins approve, hide, or delete.
type TestimonialStatus string

const (
	TestimonialPending  TestimonialStatus = "pending"
	TestimonialApproved TestimonialStatus = "approved"
	TestimonialHidden   TestimonialStatus = "hidden"
)

// Testimonial snapshots reviewer content at submit time: later account
// edits never rewrite published reviews (same rule as ServiceName).
type Testimonial struct {
	ID         uuid.UUID
	CustomerID *uuid.UUID // NULL for seeded/admin-added entries
	Name       string
	Age        int    // 0 = not shown
	PhotoPath  string // local upload under uploads/testimonials/
	PhotoURL   string // internet picture link
	Rating     int    // 1..5
	Message    string // displayed as-written in both languages
	Status     TestimonialStatus
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Initials renders the avatar fallback (first letters of the first two
// name words) when a testimonial has no photo.
func (t Testimonial) Initials() string {
	words := strings.Fields(t.Name)
	if len(words) == 0 {
		return "W"
	}
	out := ""
	for i, w := range words {
		if i == 2 {
			break
		}
		for _, r := range w {
			out += string(unicode.ToUpper(r))
			break
		}
	}
	if out == "" {
		return "W"
	}
	return out
}

// FoodCategory splits the Bu Nah menu into its two page sections.
type FoodCategory string

const (
	FoodCategoryFood     FoodCategory = "food"
	FoodCategoryBeverage FoodCategory = "beverage"
	FoodCategorySnack    FoodCategory = "snack"
)

// FoodItem is one Bu Nah menu row. Names/descriptions display as-written in
// both languages. Prices are Medium/Large variants (either may be NULL);
// single-price items (drinks, snacks) use PriceM. Variants holds
// comma-separated sub-options (pudding flavors, snack kinds).
type FoodItem struct {
	ID          uuid.UUID
	Category    FoodCategory
	Name        string
	Description string
	PriceM      *int64
	PriceL      *int64
	Variants    string
	Active      bool
	ImagePath   string // local upload under uploads/food/
	ImageURL    string // internet picture link
	Sort        int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// HasM/HasL report which price variants exist; M/L return the value or 0.
// (html/template cannot dereference pointers, hence the helpers.)
func (f FoodItem) HasM() bool { return f.PriceM != nil }
func (f FoodItem) HasL() bool { return f.PriceL != nil }
func (f FoodItem) M() int64 {
	if f.PriceM == nil {
		return 0
	}
	return *f.PriceM
}
func (f FoodItem) L() int64 {
	if f.PriceL == nil {
		return 0
	}
	return *f.PriceL
}

// HeroBanner is one homepage hero slideshow photo. Zero rows keep the
// built-in default; one active banner renders statically.
type HeroBanner struct {
	ID        uuid.UUID
	ImagePath string // local upload under uploads/hero/
	ImageURL  string // internet picture link
	Sort      int
	Active    bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// GalleryKind selects a photo tile or a linked-video tile.
type GalleryKind string

const (
	GalleryPhoto GalleryKind = "photo"
	GalleryVideo GalleryKind = "video"
)

// GalleryItem is one service-gallery row. Titles/captions display as-written
// in both languages (food precedent, no title_id). Photos use ImagePath (local
// upload under uploads/gallery/) or ImageURL; videos are links only
// (VideoURL, normalized at render) with an optional thumbnail
// (ThumbPath/ThumbURL). ServiceID NULL means unlinked (no filter chip).
type GalleryItem struct {
	ID          uuid.UUID
	Kind        GalleryKind
	Title       string
	Caption     string
	ServiceID   *uuid.UUID
	ServiceName string // joined display name, "" when unlinked
	ImagePath   string
	ImageURL    string
	VideoURL    string
	ThumbPath   string
	ThumbURL    string
	Sort        int
	Active      bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// IsVideo reports whether the tile embeds a linked video.
func (g GalleryItem) IsVideo() bool { return g.Kind == GalleryVideo }

// PromoKind selects percent-off (1-100) or fixed-IDR-off discounts.
type PromoKind string

const (
	PromoPercent PromoKind = "percent"
	PromoFixed   PromoKind = "fixed"
)

// PromoCode is a reusable discount code. MaxUses NULL means unlimited.
type PromoCode struct {
	ID        uuid.UUID
	Code      string
	Kind      PromoKind
	Value     int
	MinSpend  int64
	MaxUses   *int
	UsedCount int
	Active    bool
	ValidFrom *time.Time
	ValidTo   *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Net returns the chargeable amount after the snapshot discount.
func (b Booking) Net() int64 {
	if net := b.PriceIDR - b.DiscountIDR; net > 0 {
		return net
	}
	return 0
}

// IsGuest reports whether the booking belongs to no account.
func (b Booking) IsGuest() bool { return b.CustomerID == nil }

// OwnedBy reports whether the user owns an account booking.
func (b Booking) OwnedBy(id uuid.UUID) bool {
	return b.CustomerID != nil && *b.CustomerID == id
}

// TokenOK verifies a guest management token in constant time.
func (b Booking) TokenOK(t string) bool {
	if b.AccessToken == nil || t == "" {
		return false
	}
	a, c := *b.AccessToken, t
	if len(a) != len(c) {
		return false
	}
	n := 0
	for i := range a {
		n |= int(a[i]) ^ int(c[i])
	}
	return n == 0
}

// TokenParam returns the token for link building ("" when none).
func (b Booking) TokenParam() string {
	if b.AccessToken == nil {
		return ""
	}
	return *b.AccessToken
}

// GuestContactName prefers the guest name, falling back to account data.
func (b Booking) GuestContactName() string {
	if b.GuestName != "" {
		return b.GuestName
	}
	return b.CustomerName
}
