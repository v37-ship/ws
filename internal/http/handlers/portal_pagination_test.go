package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"

	"github.com/google/uuid"
)

// The page query param is the only untrusted input pagination adds. A junk
// value must fall back to the first page instead of reaching SQL.
func TestNormalizePage(t *testing.T) {
	cases := map[string]int{
		"":    1,
		"1":   1,
		"2":   2,
		"17":  17,
		" 3 ": 3,
		"0":   1,
		"-4":  1,
		"abc": 1,
		"2.5": 1,
		"1e3": 1,
		"+2":  2,
		"  ":  1,
	}
	for in, want := range cases {
		if got := normalizePage(in); got != want {
			t.Errorf("normalizePage(%q) = %d, want %d", in, got, want)
		}
	}
}

// TotalPages drives the pager copy and the next-link bound; an empty list still
// renders "Page 1 of 1" rather than a page count of zero.
func TestTotalPages(t *testing.T) {
	cases := map[int]int{
		-1:                  1,
		0:                   1,
		1:                   1,
		repo.PageSize:       1,
		repo.PageSize + 1:   2,
		2 * repo.PageSize:   2,
		2*repo.PageSize + 1: 3,
		30:                  2,
	}
	for total, want := range cases {
		if got := totalPages(total); got != want {
			t.Errorf("totalPages(%d) = %d, want %d", total, got, want)
		}
	}
}

// Pager links must keep the active filters and never repeat the page param the
// user is leaving behind; page 1 stays a clean URL.
func TestPageURL(t *testing.T) {
	cases := []struct {
		name  string
		query url.Values
		page  int
		want  string
	}{
		{"first page drops the param", url.Values{}, 1, "/portal/bookings"},
		{"second page", url.Values{}, 2, "/portal/bookings?page=2"},
		{"filters survive paging", url.Values{"q": {"Ana"}}, 3, "/portal/bookings?page=3&q=Ana"},
		{"stale page param replaced", url.Values{"page": {"7"}, "q": {"Ana"}}, 2, "/portal/bookings?page=2&q=Ana"},
		{"first page keeps filters", url.Values{"page": {"2"}, "q": {"Ana"}}, 1, "/portal/bookings?q=Ana"},
		{"empty form fields dropped", url.Values{"status": {""}, "q": {"Ana"}}, 2, "/portal/bookings?page=2&q=Ana"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pageURL("/portal/bookings", c.query, c.page); got != c.want {
				t.Errorf("pageURL(%v, %d) = %q, want %q", c.query, c.page, got, c.want)
			}
		})
	}
}

func pagedBooking(n int) domain.Booking {
	start := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)
	customerID := uuid.New()
	return domain.Booking{
		ID: uuid.New(), CustomerID: &customerID,
		CustomerName:  fmt.Sprintf("Customer %02d", n),
		CustomerPhone: "081200000000",
		ServiceName:   "Baby Massage", StaffName: "Bidan Ayu",
		StartAt: start, EndAt: start.Add(time.Hour),
		Status:        domain.BookingConfirmed,
		PriceIDR:      150000,
		PaymentMethod: domain.PaymentMethodQRISStatic, PaymentStatus: domain.PaymentPending,
	}
}

func pagedCustomer(n int) domain.User {
	return domain.User{
		ID: uuid.New(), Email: fmt.Sprintf("mom%02d@example.com", n),
		FullName: fmt.Sprintf("Mom %02d", n), Phone: "081200000000",
		Role: domain.RoleCustomer, IsActive: true, CreatedAt: time.Now(),
	}
}

// pageSlice copies the fake repo's windowing so the handler test exercises the
// real page -> offset contract (repo.PageSize) without a database.
func pageSlice[T any](all []T, page int) []T {
	start := (page - 1) * repo.PageSize
	if start < 0 || start >= len(all) {
		return nil
	}
	return all[start:min(start+repo.PageSize, len(all))]
}

// swapBookingPage pins the bookings list's two data calls for one test.
func swapBookingPage(t *testing.T, list func(repo.FilterParams) ([]domain.Booking, error), count func(repo.FilterParams) (int, error)) {
	t.Helper()
	prevList, prevCount := portalBookingList, portalBookingCount
	portalBookingList = func(_ *Server, _ *http.Request, f repo.FilterParams) ([]domain.Booking, error) {
		return list(f)
	}
	portalBookingCount = func(_ *Server, _ *http.Request, f repo.FilterParams) (int, error) {
		return count(f)
	}
	t.Cleanup(func() { portalBookingList, portalBookingCount = prevList, prevCount })
}

// renderBookingPage asks for the rows fragment in English via the language
// cookie, so the pager URLs contain only the filters a user actually set.
func renderBookingPage(t *testing.T, s *Server, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("HX-Request", "true")
	req.AddCookie(&http.Cookie{Name: "wangga-lang", Value: "en"})
	s.PortalBookings(rec, req)
	return rec
}

// Page 2 of a 30-row list shows rows 26-30 and nothing from page 1. The old
// silent 100-row cap is gone: the handler now asks for a page, not a limit.
func TestPortalBookingsPageTwo(t *testing.T) {
	s := newFailTestServer(t)
	all := make([]domain.Booking, 30)
	for i := range all {
		all[i] = pagedBooking(i + 1)
	}
	var gotFilter repo.FilterParams
	swapBookingPage(t,
		func(f repo.FilterParams) ([]domain.Booking, error) { return pageSlice(all, f.Page), nil },
		func(f repo.FilterParams) (int, error) { gotFilter = f; return len(all), nil },
	)

	rec := renderBookingPage(t, s, "/portal/bookings?page=2")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotFilter.Page != 2 {
		t.Errorf("list filter Page = %d, want 2", gotFilter.Page)
	}
	if gotFilter.Limit != 0 {
		t.Errorf("list filter Limit = %d, want 0 (the 100-row cap is replaced by pagination)", gotFilter.Limit)
	}
	out := rec.Body.String()
	for _, want := range []string{"Customer 26", "Customer 27", "Customer 30", "Page 2 of 2"} {
		if !contains(out, want) {
			t.Errorf("page 2 missing %q", want)
		}
	}
	for _, never := range []string{"Customer 25", "Customer 01"} {
		if contains(out, never) {
			t.Errorf("page 2 must not show %q", never)
		}
	}
	// Second and last page: next is disabled (no link), prev returns to page 1.
	if got := buttonTag(out, "Next ▶"); !contains(got, "disabled") {
		t.Errorf("page 2 of 2 next control must be disabled, opening tag = %q", got)
	}
	if contains(out, "page=3") {
		t.Error("page 2 of 2 must not offer a next link")
	}
	if !contains(out, `href="/portal/bookings"`) {
		t.Errorf("page 2 lost its prev link back to page 1")
	}
}

// A page past the end is empty, but its controls must not strand the user:
// next stays disabled and prev walks back to the last real page.
func TestPortalBookingsPageBeyondEnd(t *testing.T) {
	s := newFailTestServer(t)
	all := make([]domain.Booking, 30)
	for i := range all {
		all[i] = pagedBooking(i + 1)
	}
	swapBookingPage(t,
		func(f repo.FilterParams) ([]domain.Booking, error) { return pageSlice(all, f.Page), nil },
		func(repo.FilterParams) (int, error) { return len(all), nil },
	)

	rec := renderBookingPage(t, s, "/portal/bookings?page=3")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	out := rec.Body.String()
	for _, want := range []string{
		"No bookings found matching filters.", // honest empty state, not a blank table
		"Page 3 of 2",
		`href="/portal/bookings?page=2"`, // the way back
	} {
		if !contains(out, want) {
			t.Errorf("out-of-range page missing %q", want)
		}
	}
	if contains(out, "page=4") {
		t.Error("out-of-range page must not offer a next link")
	}
	for _, never := range []string{"Customer 01", "Customer 26"} {
		if contains(out, never) {
			t.Errorf("out-of-range page must not render rows, found %q", never)
		}
	}
}

// Filters ride the pager: paging from a search must keep the search.
func TestPortalBookingsPagerKeepsFilters(t *testing.T) {
	s := newFailTestServer(t)
	swapBookingPage(t,
		func(f repo.FilterParams) ([]domain.Booking, error) { return []domain.Booking{pagedBooking(1)}, nil },
		func(repo.FilterParams) (int, error) { return 60, nil },
	)

	rec := renderBookingPage(t, s, "/portal/bookings?q=Ana&page=2")
	out := rec.Body.String()
	if !contains(out, "q=Ana") {
		t.Errorf("pager dropped the search filter:\n%s", out)
	}
	if !contains(out, "page=3") {
		t.Errorf("middle page lost its next link")
	}
}

// First page: prev is rendered disabled, next works.
func TestPortalBookingsFirstPage(t *testing.T) {
	s := newFailTestServer(t)
	swapBookingPage(t,
		func(f repo.FilterParams) ([]domain.Booking, error) { return []domain.Booking{pagedBooking(1)}, nil },
		func(repo.FilterParams) (int, error) { return 60, nil },
	)

	rec := renderBookingPage(t, s, "/portal/bookings")
	out := rec.Body.String()
	if !contains(out, "Page 1 of 3") {
		t.Errorf("first page copy missing, got no page marker")
	}
	if got := buttonTag(out, "◀ Prev"); !contains(got, "disabled") {
		t.Errorf("first page prev control must be disabled, opening tag = %q", got)
	}
	if !contains(out, `href="/portal/bookings?page=2"`) {
		t.Errorf("first page lost its next link")
	}
}

// swapCustomerPage pins the customer directory's single data call.
func swapCustomerPage(t *testing.T, fn func(q string, page int) ([]domain.User, int, error)) {
	t.Helper()
	prev := portalCustomerList
	portalCustomerList = func(_ *Server, _ *http.Request, q string, page int) ([]domain.User, int, error) {
		return fn(q, page)
	}
	t.Cleanup(func() { portalCustomerList = prev })
}

func renderCustomerPage(t *testing.T, s *Server, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.AddCookie(&http.Cookie{Name: "wangga-lang", Value: "en"})
	s.PortalCustomers(rec, req)
	return rec
}

// The customer directory is the second unbounded list: page 2 shows rows 26-30
// of 30 with plain links (no HTMX) and the same page copy.
func TestPortalCustomersPageTwo(t *testing.T) {
	s := newFailTestServer(t)
	all := make([]domain.User, 30)
	for i := range all {
		all[i] = pagedCustomer(i + 1)
	}
	var gotQ string
	var gotPage int
	swapCustomerPage(t, func(q string, page int) ([]domain.User, int, error) {
		gotQ, gotPage = q, page
		return pageSlice(all, page), len(all), nil
	})

	rec := renderCustomerPage(t, s, "/portal/customers?q=Mom&page=2")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotPage != 2 || gotQ != "Mom" {
		t.Errorf("customer list got q=%q page=%d, want q=Mom page=2", gotQ, gotPage)
	}
	out := rec.Body.String()
	for _, want := range []string{"Mom 26", "Mom 30", "Page 2 of 2", `href="/portal/customers?q=Mom"`} {
		if !contains(out, want) {
			t.Errorf("customer page 2 missing %q", want)
		}
	}
	if contains(out, "Mom 25") || contains(out, "Mom 01") {
		t.Error("customer page 2 must not show page 1 rows")
	}
	if contains(out, `hx-get="/portal/customers`) {
		t.Error("customer pagination is plain links, not HTMX")
	}
	if contains(out, "page=3") {
		t.Error("customer page 2 of 2 must not offer a next link")
	}
}

// Same beyond-the-end contract as bookings: empty, honest copy, and a way back.
func TestPortalCustomersPageBeyondEnd(t *testing.T) {
	s := newFailTestServer(t)
	all := make([]domain.User, 30)
	for i := range all {
		all[i] = pagedCustomer(i + 1)
	}
	swapCustomerPage(t, func(q string, page int) ([]domain.User, int, error) {
		return pageSlice(all, page), len(all), nil
	})

	rec := renderCustomerPage(t, s, "/portal/customers?page=3")
	out := rec.Body.String()
	for _, want := range []string{
		"No customers found.",
		"Page 3 of 2",
		`href="/portal/customers?page=2"`,
	} {
		if !contains(out, want) {
			t.Errorf("customer out-of-range page missing %q", want)
		}
	}
	if contains(out, "page=4") {
		t.Error("customer out-of-range page must not offer a next link")
	}
	if contains(out, "Mom 26") {
		t.Error("customer out-of-range page must not render rows")
	}
}

// The bookings pager lives in the rows fragment, so it must swap with the rows
// it describes: hx-get into #booking-rows with hx-push-url.
func TestBookingPagerRidesRowsSwap(t *testing.T) {
	s := newFailTestServer(t)
	var buf bytes.Buffer
	data := pageData{
		"Lang": "en", "Page": 2, "TotalPages": 3,
		"PrevURL":  "/portal/bookings?q=Ana",
		"NextURL":  "/portal/bookings?page=3&q=Ana",
		"Bookings": []domain.Booking{pagedBooking(26)},
	}
	if err := s.tpl.ExecuteTemplate(&buf, "portal_booking_rows.html", data); err != nil {
		t.Fatalf("execute rows: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`hx-get="/portal/bookings?q=Ana"`,
		`hx-get="/portal/bookings?page=3&amp;q=Ana"`,
		`hx-target="#booking-rows"`,
		`hx-swap="innerHTML"`,
		`hx-push-url="true"`,
		"Page 2 of 3",
		"◀ Prev",
		"Next ▶",
	} {
		if !contains(out, want) {
			t.Errorf("rows pager missing %q", want)
		}
	}
	// The pager is part of the swapped fragment: it must sit inside the rendered
	// rows (a trailing table row), not outside the tbody.
	if strings.Index(out, "Page 2 of 3") < strings.Index(out, "<tr>") {
		t.Error("pager must render as table content after the rows")
	}
}

// Both dictionaries carry the pager copy; a missing key would render the key
// itself (i18n.T echoes it), which the literal assertions catch.
func TestBookingPagerCopyBothLanguages(t *testing.T) {
	s := newFailTestServer(t)
	want := map[string]string{
		"en": "Page 2 of 2",
		"id": "Halaman 2 dari 2",
	}
	for _, lang := range []string{"en", "id"} {
		var buf bytes.Buffer
		data := pageData{
			"Lang": lang, "Page": 2, "TotalPages": 2,
			"PrevURL": "/portal/bookings?page=1", "NextURL": "",
			"Bookings": []domain.Booking{pagedBooking(26)},
		}
		if err := s.tpl.ExecuteTemplate(&buf, "portal_booking_rows.html", data); err != nil {
			t.Fatalf("execute rows (%s): %v", lang, err)
		}
		if out := buf.String(); !contains(out, want[lang]) {
			t.Errorf("rows pager (%s): want %q", lang, want[lang])
		}
	}
}

// A load failure during an HTMX pagination swap must answer the Task-1 toast
// contract: the old rows stay put (no silent 500 body) and staff are told.
func TestPortalBookingsLoadFailureToasts(t *testing.T) {
	s := newFailTestServer(t)
	swapBookingPage(t,
		func(repo.FilterParams) ([]domain.Booking, error) { return nil, errors.New("db down") },
		func(repo.FilterParams) (int, error) { return 0, errors.New("db down") },
	)

	rec := renderBookingPage(t, s, "/portal/bookings?page=2")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); body != "" {
		t.Errorf("HTMX failure must not write a body, got %q", body)
	}
	if kind, _ := decodeToast(t, rec); kind != "error" {
		t.Errorf("toast kind = %q, want error", kind)
	}
}

func TestPortalCustomersLoadFailureToasts(t *testing.T) {
	s := newFailTestServer(t)
	swapCustomerPage(t, func(string, int) ([]domain.User, int, error) {
		return nil, 0, errors.New("db down")
	})

	rec := renderCustomerPage(t, s, "/portal/customers")
	if rec.Code != http.StatusOK { // plain page render: the portal error shell
		t.Fatalf("status = %d, want 200 portal error page", rec.Code)
	}
	if !contains(rec.Body.String(), `id="portal-error"`) {
		t.Error("plain customer load failure must render the portal error page")
	}
}
