//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"
	"spa-booking/internal/service"
)

// The portal pager and the staff no-double-book rule are the two behaviours
// that only exist in SQL: `bookingFilterWhere` is shared by ListFiltered and
// CountFiltered (so a filter can never mean one thing in the rows and another
// in the total), and the EXCLUDE gist in migration 003 is what actually stops
// two active bookings from sharing one therapist. The unit tests pin the query
// builders; this group executes them against real rows and the real constraint.

const (
	// pageDateA and pageDateB are fixed WIB dates: the date filter compares
	// `date(start_at AT TIME ZONE 'Asia/Jakarta')`, so the assertion must not
	// depend on when the suite runs.
	pageDateA = "2026-12-01" // 20 confirmed bookings
	pageDateB = "2026-12-02" // 10 checked_in bookings
)

// Payment-fixture plan for the checked_in day. The filters are read from the
// real SQL, not guessed:
//
//	unpaid = p.status='pending' AND b.status IN (active four)      -> 4 + 2 claims
//	claim  = unpaid AND p.customer_reported_at IS NOT NULL         -> 2
//	paid   = p.status='paid'                                       -> 4
//
// A claim row is also unpaid, so the two filters overlap by design (the claim
// queue is a subset of the money still owed). Confirmed bookings on date A have
// no payment row at all, so they count for none of the three — the LEFT JOIN's
// NULL status is not 'pending'.
const (
	pagePendingRows = 4
	pageClaimRows   = 2
	pagePaidRows    = 4
)

// paginationFixture is one branch with two therapists and 30 bookings across
// two WIB dates and two statuses, plus the real repo the assertions read.
type paginationFixture struct {
	pool     *pgxpool.Pool
	bookings *repo.BookingRepo

	loc     uuid.UUID
	service uuid.UUID
	staffA  uuid.UUID
	staffB  uuid.UUID

	confirmed []uuid.UUID // date A, status confirmed
	checkedIn []uuid.UUID // date B, status checked_in
}

// pagerStart returns 08:00 + offset hours on date, in Asia/Jakarta. Consecutive
// offsets are one-hour slots meeting at the boundary ([a,a+1h),[a+1h,a+2h)), so
// seeding ten of them per therapist satisfies the staff exclusion.
func pagerStart(t *testing.T, date string, offset int) time.Time {
	t.Helper()
	day, err := time.ParseInLocation("2006-01-02", date, wib)
	if err != nil {
		t.Fatalf("pagerStart: parse %q: %v", date, err)
	}
	return day.Add(time.Duration(8+offset) * time.Hour)
}

// newPaginationFixture truncates the schema and seeds the two dates: 20
// confirmed rows on date A (ten hourly slots each for two therapists) and 10
// checked_in rows on date B (five slots each), with the payment mix above.
func newPaginationFixture(t *testing.T) *paginationFixture {
	t.Helper()
	pool := OpenTestDB(t)
	TruncateAll(t, pool)

	loc := insertLocation(t, pool, "Pager Branch")
	fx := &paginationFixture{
		pool:     pool,
		bookings: repo.NewBookingRepo(pool),
		loc:      loc,
		service:  insertService(t, pool, loc, "Pager Treatment", 150_000),
		staffA:   insertStaff(t, pool, loc, "Pager Therapist A"),
		staffB:   insertStaff(t, pool, loc, "Pager Therapist B"),
	}

	for i := 0; i < 20; i++ {
		staff := fx.staffA
		if i >= 10 {
			staff = fx.staffB
		}
		id := insertBooking(t, pool, bookingSeed{
			LocationID: loc,
			ServiceID:  fx.service,
			StaffID:    staff,
			StartAt:    pagerStart(t, pageDateA, i%10),
			PriceIDR:   150_000,
			Status:     string(domain.BookingConfirmed),
			GuestName:  fmt.Sprintf("Pager Guest A%02d", i+1),
		})
		fx.confirmed = append(fx.confirmed, id)
	}

	for i := 0; i < 10; i++ {
		staff := fx.staffA
		if i >= 5 {
			staff = fx.staffB
		}
		id := insertBooking(t, pool, bookingSeed{
			LocationID: loc,
			ServiceID:  fx.service,
			StaffID:    staff,
			StartAt:    pagerStart(t, pageDateB, i%5),
			PriceIDR:   150_000,
			Status:     string(domain.BookingCheckedIn),
			GuestName:  fmt.Sprintf("Pager Guest B%02d", i+1),
		})
		fx.checkedIn = append(fx.checkedIn, id)

		switch {
		case i < pagePendingRows:
			insertPayment(t, pool, paymentSeed{BookingID: id, AmountIDR: 150_000, Status: string(domain.PaymentPending)})
		case i < pagePendingRows+pageClaimRows:
			now := time.Now()
			insertPayment(t, pool, paymentSeed{
				BookingID:          id,
				AmountIDR:          150_000,
				Status:             string(domain.PaymentPending),
				CustomerReportedAt: &now,
			})
		default:
			insertPayment(t, pool, paymentSeed{BookingID: id, AmountIDR: 150_000})
		}
	}

	return fx
}

// listAllPages walks the portal's page window (Page 1, 2, …) for one filter
// until a page comes back short, returning the rows and each page's size. A
// full page never means the end, so a 50-row result also visits page 3 (empty).
func listAllPages(t *testing.T, bookings *repo.BookingRepo, f repo.FilterParams) ([]domain.Booking, []int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var (
		rows  []domain.Booking
		sizes []int
	)
	for page := 1; page <= 100; page++ {
		f.Page = page
		got, err := bookings.ListFiltered(ctx, f)
		if err != nil {
			t.Fatalf("list page %d: %v", page, err)
		}
		sizes = append(sizes, len(got))
		rows = append(rows, got...)
		if len(got) < repo.PageSize {
			break
		}
	}
	return rows, sizes
}

// idsOf projects a page into ids.
func idsOf(rows []domain.Booking) []uuid.UUID {
	ids := make([]uuid.UUID, len(rows))
	for i, b := range rows {
		ids[i] = b.ID
	}
	return ids
}

// idSet collapses ids for disjoint-union assertions.
func idSet(ids []uuid.UUID) map[uuid.UUID]struct{} {
	set := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

// TestBookingPaginationMatchesCount proves the shared predicate really is
// shared: for every filter the count equals what walking the pages can reach,
// the pages never overlap, every row satisfies the filter, and a page never
// exceeds the portal's 25-row window. The unfiltered case also pins the exact
// page math (25 then 5).
func TestBookingPaginationMatchesCount(t *testing.T) {
	fx := newPaginationFixture(t)
	if repo.PageSize != 25 {
		t.Fatalf("repo.PageSize = %d, want the portal's 25", repo.PageSize)
	}

	statusIs := func(want domain.BookingStatus) func(domain.Booking) bool {
		return func(b domain.Booking) bool { return b.Status == want }
	}
	paidIs := func(want domain.PaymentStatus) func(domain.Booking) bool {
		return func(b domain.Booking) bool { return b.PaymentStatus == want }
	}

	cases := []struct {
		name   string
		filter repo.FilterParams
		want   int
		match  func(domain.Booking) bool
	}{
		{
			name:   "unfiltered",
			filter: repo.FilterParams{},
			want:   len(fx.confirmed) + len(fx.checkedIn),
		},
		{
			name:   "status confirmed",
			filter: repo.FilterParams{Status: domain.BookingConfirmed},
			want:   len(fx.confirmed),
			match:  statusIs(domain.BookingConfirmed),
		},
		{
			name:   "status checked_in",
			filter: repo.FilterParams{Status: domain.BookingCheckedIn},
			want:   len(fx.checkedIn),
			match:  statusIs(domain.BookingCheckedIn),
		},
		{
			name:   "date A (Jakarta day)",
			filter: repo.FilterParams{Date: pageDateA},
			want:   len(fx.confirmed),
			match: func(b domain.Booking) bool {
				return b.StartAt.In(wib).Format("2006-01-02") == pageDateA
			},
		},
		{
			name:   "date B (Jakarta day)",
			filter: repo.FilterParams{Date: pageDateB},
			want:   len(fx.checkedIn),
			match: func(b domain.Booking) bool {
				return b.StartAt.In(wib).Format("2006-01-02") == pageDateB
			},
		},
		{
			// Filters compose with AND: no confirmed booking sits on date B.
			name:   "date B and status confirmed",
			filter: repo.FilterParams{Date: pageDateB, Status: domain.BookingConfirmed},
			want:   0,
		},
		{
			name:   "search on the guest name",
			filter: repo.FilterParams{Search: "pager guest"},
			want:   len(fx.confirmed) + len(fx.checkedIn),
			match: func(b domain.Booking) bool {
				return strings.Contains(strings.ToLower(b.CustomerName), "pager guest")
			},
		},
		{
			// The LEFT JOIN's NULL status is not 'pending', so only the rows
			// with a payment row count: 4 pending + the 2 claim rows.
			name:   "payment unpaid (claim subset included)",
			filter: repo.FilterParams{Payment: "unpaid"},
			want:   pagePendingRows + pageClaimRows,
			match:  paidIs(domain.PaymentPending),
		},
		{
			name:   "payment claim",
			filter: repo.FilterParams{Payment: "claim"},
			want:   pageClaimRows,
			match:  paidIs(domain.PaymentPending),
		},
		{
			name:   "payment paid",
			filter: repo.FilterParams{Payment: "paid"},
			want:   pagePaidRows,
			match:  paidIs(domain.PaymentPaid),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			count, err := fx.bookings.CountFiltered(ctx, c.filter)
			if err != nil {
				t.Fatalf("count: %v", err)
			}
			if count != c.want {
				t.Fatalf("CountFiltered = %d, want %d", count, c.want)
			}

			rows, sizes := listAllPages(t, fx.bookings, c.filter)
			if len(rows) != count {
				t.Fatalf("pages reach %d rows (%v), count says %d", len(rows), sizes, count)
			}
			// No row may appear on two pages: a missing ORDER tiebreak or a
			// wrong OFFSET would show up here as a duplicate id.
			if set := idSet(idsOf(rows)); len(set) != len(rows) {
				t.Fatalf("pages overlap: %d rows but %d distinct ids", len(rows), len(set))
			}
			// Every page but the last (short) one is exactly one window.
			for i, size := range sizes {
				if size > repo.PageSize {
					t.Fatalf("page %d returned %d rows, over the %d-row window", i+1, size, repo.PageSize)
				}
			}
			if c.match != nil {
				for _, b := range rows {
					if !c.match(b) {
						t.Fatalf("row %s does not satisfy the %q filter (status=%s payment=%s start=%s)",
							b.ID, c.name, b.Status, b.PaymentStatus, b.StartAt.In(wib).Format(time.RFC3339))
					}
				}
			}
		})
	}

	t.Run("unfiltered page math is 25 then 5", func(t *testing.T) {
		_, sizes := listAllPages(t, fx.bookings, repo.FilterParams{})
		if len(sizes) != 2 || sizes[0] != 25 || sizes[1] != 5 {
			t.Fatalf("page sizes = %v, want [25 5]", sizes)
		}
	})

	t.Run("pages partition the seeded set", func(t *testing.T) {
		rows, _ := listAllPages(t, fx.bookings, repo.FilterParams{})
		got := idSet(idsOf(rows))
		seeded := append(append([]uuid.UUID{}, fx.confirmed...), fx.checkedIn...)
		want := idSet(seeded)
		if len(got) != len(want) {
			t.Fatalf("union has %d ids, seeded %d", len(got), len(want))
		}
		for id := range want {
			if _, ok := got[id]; !ok {
				t.Fatalf("seeded booking %s never appeared on a page", id)
			}
		}
	})
}

// TestBookingPageBeyondEnd pins the empty tail: page 3 holds nothing but the
// count stays at the filter's total, including when the count query receives
// the same paged filter the handler sends it.
func TestBookingPageBeyondEnd(t *testing.T) {
	fx := newPaginationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	total, err := fx.bookings.CountFiltered(ctx, repo.FilterParams{})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 30 {
		t.Fatalf("unfiltered count = %d, want 30", total)
	}

	past, err := fx.bookings.ListFiltered(ctx, repo.FilterParams{Page: 3})
	if err != nil {
		t.Fatalf("page 3 list: %v", err)
	}
	if len(past) != 0 {
		t.Fatalf("page 3 returned %d rows, want none", len(past))
	}

	// The handler builds one FilterParams (Page included) and hands it to both
	// the count and the list; the count must ignore the window.
	filter := repo.FilterParams{Status: domain.BookingConfirmed, Page: 2}
	count, err := fx.bookings.CountFiltered(ctx, filter)
	if err != nil {
		t.Fatalf("count paged filter: %v", err)
	}
	if count != len(fx.confirmed) {
		t.Fatalf("CountFiltered with Page=2 = %d, want %d (the page window must not shrink the total)", count, len(fx.confirmed))
	}
	rows, err := fx.bookings.ListFiltered(ctx, filter)
	if err != nil {
		t.Fatalf("list paged filter: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("confirmed page 2 returned %d rows, want none (only %d confirmed rows)", len(rows), len(fx.confirmed))
	}

	// Page 0 is the non-paginated path the dashboard day list and the digest
	// use: no window at all, and Limit stays a cap rather than a page.
	unpaged, err := fx.bookings.ListFiltered(ctx, repo.FilterParams{})
	if err != nil {
		t.Fatalf("unpaged list: %v", err)
	}
	if len(unpaged) != total {
		t.Fatalf("unpaged list returned %d rows, want all %d", len(unpaged), total)
	}
	capped, err := fx.bookings.ListFiltered(ctx, repo.FilterParams{Limit: 5})
	if err != nil {
		t.Fatalf("limit 5 list: %v", err)
	}
	if len(capped) != 5 {
		t.Fatalf("Limit=5 list returned %d rows, want 5", len(capped))
	}
}

// TestStaffNoDoubleBookExclusion executes the migration-003 EXCLUDE constraint:
// the second active booking on one therapist's slot fails with Postgres 23P01,
// the service maps that to ErrConflict, and the inactive/edge cases (cancelled,
// completed, back-to-back) keep working. This is the rule the portal relies on
// instead of a read-then-write availability check.
func TestStaffNoDoubleBookExclusion(t *testing.T) {
	pool := OpenTestDB(t)
	TruncateAll(t, pool)

	loc := insertLocation(t, pool, "Exclusion Branch")
	serviceID := insertService(t, pool, loc, "Exclusion Treatment", 200_000)
	staff := insertStaff(t, pool, loc, "Exclusion Therapist")
	customer := insertUser(t, pool, "exclusion@example.com", string(domain.RoleCustomer))
	bookings := repo.NewBookingRepo(pool)
	// The undo group's constructor is the production wiring (real repos,
	// qris_manual so CreatePending inserts a confirmed row without a charge,
	// mock WhatsApp). Reusing it keeps the creation path the one CS runs.
	svc := newUndoService(t, pool)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 14:00-15:00 WIB tomorrow, through the real service.
	start := pagerStart(t, "2026-12-15", 6)
	first, _, err := svc.CreatePending(ctx, customer, loc, serviceID, staff, start,
		"exclusion fixture", domain.PlaceOnsite, "", "", nil)
	if err != nil {
		t.Fatalf("first booking: %v", err)
	}
	if first.Status != domain.BookingConfirmed {
		t.Fatalf("first booking status = %q, want confirmed", first.Status)
	}

	// The raw constraint: a direct overlapping insert must answer 23P01 so the
	// service's isExclusionViolation has something real to recognise.
	overlap := &domain.Booking{
		CustomerID: &customer,
		LocationID: loc,
		ServiceID:  serviceID,
		StaffID:    staff,
		StartAt:    start.Add(30 * time.Minute),
		EndAt:      start.Add(90 * time.Minute),
		Status:     domain.BookingConfirmed,
		PriceIDR:   200_000,
	}
	err = bookings.Create(ctx, overlap)
	if err == nil {
		t.Fatal("overlapping active booking was created, want the staff exclusion violation")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("overlap error %T %v is not a pgconn.PgError", err, err)
	}
	if pgErr.Code != "23P01" {
		t.Fatalf("overlap SQLSTATE = %q, want 23P01 (exclusion_violation)", pgErr.Code)
	}
	if pgErr.ConstraintName != "bookings_staff_no_overlap" {
		t.Fatalf("overlap constraint = %q, want bookings_staff_no_overlap", pgErr.ConstraintName)
	}

	// Same violation through the service: the customer-facing path must see
	// ErrConflict, not a raw pg error.
	_, _, err = svc.CreatePending(ctx, customer, loc, serviceID, staff, start.Add(30*time.Minute),
		"overlapping fixture", domain.PlaceOnsite, "", "", nil)
	if !errors.Is(err, service.ErrConflict) {
		t.Fatalf("overlapping CreatePending error = %v, want service.ErrConflict", err)
	}

	// Back-to-back is a different slot: [15:00,16:00) after [14:00,15:00).
	if _, _, err := svc.CreatePending(ctx, customer, loc, serviceID, staff, start.Add(time.Hour),
		"back to back fixture", domain.PlaceOnsite, "", "", nil); err != nil {
		t.Fatalf("back-to-back booking: %v", err)
	}

	// A cancelled row does not reserve the slot (the predicate only covers the
	// four active statuses), so a confirmed booking may take the same window.
	cancelledStart := start.Add(2 * time.Hour)
	cancelledID := insertBooking(t, pool, bookingSeed{
		CustomerID: &customer,
		LocationID: loc,
		ServiceID:  serviceID,
		StaffID:    staff,
		StartAt:    cancelledStart,
		Status:     string(domain.BookingCancelled),
		PriceIDR:   200_000,
	})
	if _, _, err := svc.CreatePending(ctx, customer, loc, serviceID, staff, cancelledStart,
		"over cancelled fixture", domain.PlaceOnsite, "", "", nil); err != nil {
		t.Fatalf("booking over a cancelled slot: %v", err)
	}

	// Completed and no_show are inactive too.
	for i, status := range []domain.BookingStatus{domain.BookingCompleted, domain.BookingNoShow} {
		slot := start.Add(time.Duration(3+i) * time.Hour)
		insertBooking(t, pool, bookingSeed{
			CustomerID: &customer,
			LocationID: loc,
			ServiceID:  serviceID,
			StaffID:    staff,
			StartAt:    slot,
			Status:     string(status),
			PriceIDR:   200_000,
		})
		if _, _, err := svc.CreatePending(ctx, customer, loc, serviceID, staff, slot,
			"over inactive fixture", domain.PlaceOnsite, "", "", nil); err != nil {
			t.Fatalf("booking over a %s slot: %v", status, err)
		}
	}

	// A pending_payment hold is active and must block just like confirmed ones.
	holdStart := start.Add(6 * time.Hour)
	insertBooking(t, pool, bookingSeed{
		CustomerID: &customer,
		LocationID: loc,
		ServiceID:  serviceID,
		StaffID:    staff,
		StartAt:    holdStart,
		Status:     string(domain.BookingPendingPayment),
		PriceIDR:   200_000,
	})
	_, _, err = svc.CreatePending(ctx, customer, loc, serviceID, staff, holdStart.Add(15*time.Minute),
		"over a hold fixture", domain.PlaceOnsite, "", "", nil)
	if !errors.Is(err, service.ErrConflict) {
		t.Fatalf("booking over a pending_payment hold error = %v, want service.ErrConflict", err)
	}

	// Only the successful creations exist: 1 first + 1 back-to-back + 3 over the
	// inactive rows + the 3 inactive/cancelled rows seeded directly + 1 hold.
	// The three rejected attempts wrote nothing.
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM bookings WHERE staff_id=$1`, staff).Scan(&rows); err != nil {
		t.Fatalf("count bookings: %v", err)
	}
	const wantRows = 1 + 1 + 3 + 3 + 1
	if rows != wantRows {
		t.Fatalf("bookings for the therapist = %d, want %d (rejected creates must leave no row)", rows, wantRows)
	}
	var cancelledStill int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM bookings WHERE id=$1 AND status='cancelled'`, cancelledID).Scan(&cancelledStill); err != nil {
		t.Fatalf("reload cancelled: %v", err)
	}
	if cancelledStill != 1 {
		t.Fatalf("cancelled row changed: %d", cancelledStill)
	}
}
