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

// Payroll vocabulary. A rate row's cadence decides which period kind pays it,
// and a payout snapshot is keyed by that same kind, so cadence and period kind
// share one set of values (the migration's CHECK constraints list them too).
const (
	PeriodWeekly  = "weekly"
	PeriodMonthly = "monthly"

	CompCadenceWeekly  = PeriodWeekly
	CompCadenceMonthly = PeriodMonthly

	CompBaseList = "list"
	CompBaseNet  = "net"
)

// StaffComp is one row of a staff member's rate history. Rows are append-only:
// a raise or a change of base is a new row with a later EffectiveFrom, never an
// edit, so any past period still resolves to the rate that was in force then
// (effective_from may also be in the future, queueing a raise). PercentBp is a
// commission in basis points of a percent (2500 = 25.00%); FixedIDR is the
// per-cadence fixed component.
type StaffComp struct {
	ID             uuid.UUID
	StaffID        uuid.UUID
	PercentBp      int
	FixedIDR       int64
	Cadence        string
	CommissionBase string
	EffectiveFrom  time.Time // date-only
	CreatedBy      *uuid.UUID
	CreatedAt      time.Time
}

// StaffPayout is the immutable snapshot written when a staff member's period is
// marked paid: the numbers are copied in, never recomputed later, so a rate
// change or a late refund cannot rewrite a closed period (corrections are a new
// payout row with a note). CommissionIDR and TotalIDR may be negative — a late
// refund for an earlier visit can land in a period with no visits.
type StaffPayout struct {
	ID            uuid.UUID
	StaffID       uuid.UUID
	PeriodKind    string
	PeriodStart   time.Time // date-only
	PeriodEnd     time.Time // date-only
	GrossIDR      int64
	PercentBp     int
	FixedIDR      int64
	CommissionIDR int64
	TotalIDR      int64
	Note          string
	PaidBy        *uuid.UUID
	PaidAt        time.Time
}

// ValidCadence reports whether s is a payroll cadence. It mirrors the
// staff_comp/staff_payouts CHECK constraints so form validation and the
// database agree on the same two values.
func ValidCadence(s string) bool {
	return s == CompCadenceWeekly || s == CompCadenceMonthly
}

// ValidCommissionBase reports whether s is a commission base (list price or net
// of discount).
func ValidCommissionBase(s string) bool {
	return s == CompBaseList || s == CompBaseNet
}

// ValidPercentBp reports whether bp is a valid commission (0–100.00%).
func ValidPercentBp(bp int) bool { return bp >= 0 && bp <= 10000 }

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

// MonthNet is one month's cash-basis net in the revenue sparkline's series.
// Months run oldest → newest in MonthTrend.Series, which keeps the SVG builder
// a straight left-to-right plot.
type MonthNet struct {
	Month  string // YYYY-MM, Asia/Jakarta
	NetIDR int64
}

// MonthTrend is the revenue tab's trend layer: the month-to-date net compared
// with the same day-of-month last month, plus the six-month net series the
// sparkline plots. PctBp is the change in basis points of a percent (+5000 =
// +50.0%); OK is false when last month has no positive baseline (a fresh month,
// or a month that only ever refunded), in which case the page says so instead
// of inventing a percentage. AsOf is the WIB date the comparison runs through,
// canonical YYYY-MM-DD — the page renders it as "per <day> <month>" in the
// request language.
type MonthTrend struct {
	PctBp       int
	AsOf        string // YYYY-MM-DD, WIB
	OK          bool
	CurrentIDR  int64
	PreviousIDR int64
	Series      []MonthNet // oldest first
}

// DailySummary is one WIB day's operating picture for the reports page's Daily
// tab. Counts are by the day's visits (bookings whose start_at falls on the
// day); money is cash-basis by paid_at on that same WIB day, so it never
// follows the visit date. Upcoming is only today's remaining confirmed rows.
type DailySummary struct {
	Date      string // YYYY-MM-DD, Asia/Jakarta
	Completed int
	WalkIns   int
	NoShows   int
	Cancelled int
	CashIDR   int64
	QRISIDR   int64
	Upcoming  []ReportLine // today's remaining confirmed rows, by start time
}

// CollectedIDR is the day's money collected: cash plus the non-cash (QRIS)
// payments, the same split DailyPayments returns. The daily card shows this
// total as its hero and the split underneath; as a method it keeps the handler
// from doing arithmetic the service already owns.
func (d DailySummary) CollectedIDR() int64 { return d.CashIDR + d.QRISIDR }

// ReportDemand is the monthly business-health block (the reports page's
// Operations tab). Every field comes from columns a booking already snapshots
// (created_at, start_at, source, place, discount_idr, distance_fee_idr) or from
// its payment, so the block never needs a schema change.
//
// Two date bases, stated on the page:
//   - Funnel: BookingsCreated, Cancelled, NoShows bucket by created_at (the
//     date the booking was made), and BookingsPaid by paid_at.
//   - Visit mix: Visits, the four source counts, HomecareVisits and the two
//     money snapshots bucket by start_at (the visit date), whatever the status.
//
// Rates are integer basis points (1 bp = 0.01%). A zero denominator leaves the
// rate 0 and the template shows "—" rather than a misleading 0%.
type ReportDemand struct {
	// GrossPaid is copied from the month's ReportSummary, never re-derived.
	GrossPaid int64

	BookingsCreated int // bookings created this month
	BookingsPaid    int // bookings whose paid payment landed this month
	Cancelled       int // created this month, cancelled
	NoShows         int // created this month, no-show

	// No-show rate: no-shows among visits that reached a terminal state.
	NoShowVisits   int
	TerminalVisits int // completed + cancelled + no_show visits in the month
	NoShowRateBp   int

	Visits          int // bookings whose visit date falls in the month
	SourceWeb       int
	SourceWalkin    int
	SourceWhatsapp  int
	SourcePhone     int
	HomecareVisits  int
	HomecareShareBp int
	PromoDiscIDR    int64
	TransportIDR    int64
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

	// Utilization (the staff tab only; zero on every other read).
	// ScheduledMin is the weekly staff_hours expanded over the month's WIB days,
	// minus the time-off overlap with those windows. BookedMin is the completed
	// visits' booked minutes (the booking's own start/end, not the catalog
	// duration). RpPerHour divides the completed visits' service gross by
	// ScheduledMin/60; OccupancyBp divides BookedMin by ScheduledMin in basis
	// points. A zero schedule leaves both ratios 0 and the page renders "—".
	ScheduledMin int
	BookedMin    int
	RpPerHour    int64
	OccupancyBp  int
}

// TimeSpan is one [StartAt, EndAt) range. It carries staff_time_off rows and,
// in the utilization math, the expanded schedule windows.
type TimeSpan struct {
	StartAt time.Time
	EndAt   time.Time
}

// StaffUtilization is one staff member's raw utilization inputs for a month:
// the completed visits' minutes and service gross, the weekly schedule, and the
// time-off ranges overlapping the month. The ratios are derived by the service
// (integer math, never SQL), so a zero schedule can never become NaN.
type StaffUtilization struct {
	StaffID         uuid.UUID
	Name            string
	BookedMin       int
	ServiceGrossIDR int64
	Hours           []StaffHours
	TimeOff         []TimeSpan
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
