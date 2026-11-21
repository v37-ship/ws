package domain

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
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

// StatusLogPrefix opens every audit row written for a status transition. The
// audit log stores only the new status, so undo derives the previous one by
// parsing these rows (see service.previousStatusFromLogs).
const StatusLogPrefix = "Status changed to "

// StatusLogAction is the audit action text written for a transition to s. Both
// the repo (every UpdateStatusWithLog) and the creation paths (which record the
// status a booking is born with) use it, so their rows parse identically.
func StatusLogAction(s BookingStatus) string { return StatusLogPrefix + string(s) }

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
	ID             uuid.UUID
	CustomerID     *uuid.UUID // nil = guest booking (identity in Guest* fields)
	GuestName      string
	GuestPhone     string
	GuestEmail     string
	AccessToken    *string // guest management token, nil = none/revoked
	LocationID     uuid.UUID
	ServiceID      uuid.UUID
	StaffID        uuid.UUID
	StartAt        time.Time
	EndAt          time.Time
	Status         BookingStatus
	PriceIDR       int64
	BookingFeeIDR  int64
	PromoCode      string // applied promo code snapshot ('' = none)
	DiscountIDR    int64  // discount snapshot; net charged = PriceIDR - DiscountIDR
	HoldExpiresAt  *time.Time
	Notes          string
	Place          Place    // 'onsite' (at branch) or 'homecare' (customer address)
	VisitAddress   string   // required when Place is homecare
	DistanceKm     *float64 // chosen transport band's upper bound (nil = no fee)
	DistanceFeeIDR int64    // snapshot of the homecare transport fee
	Source         string   // 'web', 'walkin', 'whatsapp', 'phone'
	CreatedByID    *uuid.UUID
	UpdatedByID    *uuid.UUID
	CreatedAt      time.Time
	UpdatedAt      time.Time
	CustomerName   string
	CustomerPhone  string
	CustomerEmail  string
	ServiceName    string
	StaffName      string
	LocationName   string
	BabyName       string
	MomCondition   string
	PaymentMethod  PaymentMethod
	PaymentStatus  PaymentStatus
	// HasReceipt reports whether the booking's payment carries an uploaded
	// transfer proof. Populated by BookingRepo.ListFiltered (the queue and
	// bookings lists); zero on rows from other queries.
	HasReceipt bool
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

// WaSettings is the single-row admin WhatsApp configuration. Every field is a
// pointer: NULL means "fall back to the env default", so the row starts empty
// and only the fields an admin changed are stored here.
type WaSettings struct {
	CustomerEnabled *bool
	ReminderEnabled *bool
	RemindHour      *int
	ReviewEnabled   *bool
	ReviewURL       *string
	GroupMode       *string
	StaffGroupJID   *string
	DigestEnabled   *bool
	DigestHour      *int
	OutboundEnabled *bool
	UpdatedBy       *uuid.UUID
	UpdatedAt       time.Time
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

// DistanceZone is a homecare transport fee band: a one-way distance range
// from the branch to the customer's address, priced round-trip.
type DistanceZone struct {
	ID        uuid.UUID
	MinKM     float64
	MaxKM     float64
	FeeIDR    int64
	Active    bool
	Sort      int
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TVToken is one waiting-room kiosk link. The token is the only credential
// the TV presents, it is bound to the branch whose queue it may show, and
// revoking sets RevokedAt (a revoked row is dead immediately — the lookup
// filters on it, so there is nothing to expire).
type TVToken struct {
	ID         uuid.UUID
	Token      string
	Label      string
	LocationID uuid.UUID
	CreatedAt  *time.Time
	RevokedAt  *time.Time
}

// Active reports whether the token may still open the kiosk.
func (t TVToken) Active() bool { return t.RevokedAt == nil }

// TokenOK verifies a presented kiosk token in constant time. Revocation is
// Active()'s call, not this one: the question here is only "is this the token
// on the row", exactly like Booking.TokenOK.
func (t TVToken) TokenOK(tok string) bool {
	if t.Token == "" || tok == "" {
		return false
	}
	a, c := t.Token, tok
	if len(a) != len(c) {
		return false
	}
	n := 0
	for i := range a {
		n |= int(a[i]) ^ int(c[i])
	}
	return n == 0
}

// TVPairing is one waiting-room pairing handshake: the code the TV shows, the
// pending key that identifies the screen (its HttpOnly cookie), and the device
// the handshake eventually mints. LocationID is bound at code entry (the branch
// the CS picked), so it is empty while the pairing waits; DeviceID is set when
// the device is created and linked (ruling #6).
type TVPairing struct {
	ID         uuid.UUID
	PendingKey string
	Code       string
	LocationID *uuid.UUID
	DeviceID   *uuid.UUID
	ExpiresAt  time.Time
	ConsumedAt time.Time
	CreatedAt  time.Time
}

// Live reports whether the code may still be claimed: unconsumed and before
// its expiry. The repo enforces the same predicate atomically in Consume; this
// is the read-side twin the pair-state poll uses, so a code dies on screen the
// same way it dies in the database.
func (p TVPairing) Live() bool {
	return p.ConsumedAt.IsZero() && p.ExpiresAt.After(time.Now())
}

// TVDevice is one paired waiting-room screen. Only the hash of its secret is
// ever stored (ruling #9: a 30-day credential does not get the plaintext
// tv_tokens treatment); revoking sets RevokedAt, which kills the screen on its
// next poll.
type TVDevice struct {
	ID         uuid.UUID
	SecretHash string
	Label      string
	LocationID uuid.UUID
	PairedBy   *uuid.UUID
	PairedAt   *time.Time
	LastSeen   *time.Time
	RevokedAt  *time.Time
}

// Active reports whether the device may still open the kiosk.
func (d TVDevice) Active() bool { return d.RevokedAt == nil }

// TVSecretHash is the at-rest shape of a device secret: lowercase hex sha256.
// The portal writes it (hashing the pairing's pending key, which the TV already
// holds) and SecretOK verifies against it, so the two cannot drift apart.
func TVSecretHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// SecretOK verifies a presented device secret: sha256(secret) is compared with
// the stored hash in constant time. Revocation is Active()'s call, not this
// one (the TVToken.TokenOK split).
func (d TVDevice) SecretOK(secret string) bool {
	if d.SecretHash == "" || secret == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(d.SecretHash), []byte(TVSecretHash(secret))) == 1
}

// TVDeviceCookie packs the tv_device cookie value: the device id, a dot, then
// the secret. The id selects the row (ByID) and the secret proves it; keeping
// both in one value means a stolen device id alone authenticates nothing.
func TVDeviceCookie(id uuid.UUID, secret string) string {
	return id.String() + "." + secret
}

// ParseTVDeviceCookie splits a tv_device cookie back into its parts. The id
// must parse and the secret must be non-empty; anything else fails closed so a
// malformed cookie can never authenticate the zero device.
func ParseTVDeviceCookie(v string) (uuid.UUID, string, bool) {
	idPart, secret, ok := strings.Cut(v, ".")
	if !ok || idPart == "" || secret == "" {
		return uuid.Nil, "", false
	}
	id, err := uuid.Parse(idPart)
	if err != nil {
		return uuid.Nil, "", false
	}
	return id, secret, true
}

// Label renders the band as "6–7 km" (no trailing .0).
func (z DistanceZone) Label() string {
	return KmLabel(z.MinKM) + "–" + KmLabel(z.MaxKM) + " km"
}

// KmLabel trims a distance to its shortest decimal form ("6", "6.5").
func KmLabel(km float64) string {
	return strconv.FormatFloat(km, 'f', -1, 64)
}

// Net returns the chargeable amount: treatment + transport − discount.
func (b Booking) Net() int64 {
	if net := b.PriceIDR + b.DistanceFeeIDR - b.DiscountIDR; net > 0 {
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
