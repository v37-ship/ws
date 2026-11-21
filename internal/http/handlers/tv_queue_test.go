package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"

	"github.com/google/uuid"
)

// tvQueueWIB is the kiosk's wall clock: every label and "today" boundary is
// computed in WIB, exactly like the portal dashboard's day scope.
var tvQueueWIB = time.FixedZone("WIB", 7*3600)

// tvQueueNow is a fixed instant so the band tests never depend on the hour the
// suite runs: fixtures sit before/after 10:00 WIB on the same day.
var tvQueueNow = time.Date(2026, 11, 21, 10, 0, 0, 0, tvQueueWIB)

// swapTVQueue pins the kiosk's single data dependency and optionally records
// the filter the builder asked for. The repos are concrete pgx types, so this
// is the same seam pattern as queueBookings.
func swapTVQueue(t *testing.T, rows []domain.Booking, err error, got *repo.FilterParams) {
	t.Helper()
	prev := tvQueueBookings
	tvQueueBookings = func(_ *Server, _ context.Context, f repo.FilterParams) ([]domain.Booking, error) {
		if got != nil {
			*got = f
		}
		return rows, err
	}
	t.Cleanup(func() { tvQueueBookings = prev })
}

// swapTVClock freezes the builder's "now".
func swapTVClock(t *testing.T, now time.Time) {
	t.Helper()
	prev := tvClock
	tvClock = func() time.Time { return now }
	t.Cleanup(func() { tvClock = prev })
}

// tvSweepData feeds the generic page/partial sweeps the three bands, so the
// missing-key and emoji scans see every tvRow branch (and both status labels
// that differ between the bands).
func tvSweepData(lang string) pageData {
	now := tvRow{
		TimeLabel: "10:00", NameRedacted: "Sari W.", ServiceName: "Pijat Balinese",
		StaffFirst: "Ayu", StatusLabel: "tv.st.svc",
	}
	next := []tvRow{
		{TimeLabel: "10:30", NameRedacted: "Dwi P.", ServiceName: "Baby Massage", StaffFirst: "Bidan", StatusLabel: "tv.st.arr"},
		{TimeLabel: "11:00", NameRedacted: "Rina K.", ServiceName: "Pijat Balinese", StaffFirst: "Ayu", StatusLabel: "tv.st.conf"},
	}
	day := append(append([]tvRow{}, now), next...)
	day = append(day, tvRow{
		TimeLabel: "09:00", NameRedacted: "Nur A.", ServiceName: "Baby Massage",
		StaffFirst: "Bidan", StatusLabel: "tv.st.done",
	})
	priceM, priceL := int64(25000), int64(35000)
	return pageData{
		"Lang": lang, "TVToken": tvTestToken, "Now": []tvRow{now}, "Next": next, "Day": day,
		// The default shell. Set explicitly so the sweep renders the rotate deck
		// (and its slide partials) rather than falling through to the queue-only
		// branch on a missing key.
		"Layout": "rotate",
		// Marketing data for the slide partials: the sweeps must reach every
		// slide (and the QR include) in both languages.
		"Services": []domain.Service{{Name: "Pijat Balinese", NameID: "Pijat Bali", PriceIDR: 275000, DurationMin: 90}},
		"Testimonials": []domain.Testimonial{{
			Name: "Rina Kartika", Rating: 5, Message: "Enak sekali, bayi saya tenang.",
		}},
		"Food":      []domain.FoodItem{{Name: "Nasgor Bu Nah", PriceM: &priceM, PriceL: &priceL}},
		"ReviewURL": tvReviewTestURL,
	}
}

// tvQueueBooking builds a booking the way ListFiltered returns one: the joined
// display names are populated, because those are the only name sources the
// redactor is allowed to read.
func tvQueueBooking(name, svc, staff string, status domain.BookingStatus, place domain.Place, hour, min int) domain.Booking {
	return domain.Booking{
		ID:           uuid.New(),
		CustomerName: name,
		ServiceName:  svc,
		StaffName:    staff,
		Status:       status,
		Place:        place,
		StartAt:      time.Date(2026, 11, 21, hour, min, 0, 0, tvQueueWIB),
		EndAt:        time.Date(2026, 11, 21, hour, min, 0, 0, tvQueueWIB).Add(90 * time.Minute),
	}
}

// Redaction edge rules: first token + last token's initial; a single token
// survives untouched (nothing to initial); a one-character surname is shown
// as-is because "W." from "W" would invent punctuation.
func TestRedactName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Sari Wijaya", "Sari W."},
		{"Sari", "Sari"},
		{"Sari W", "Sari W"},
		{"Sari Wijaya Putri", "Sari P."},
		{"  Sari   Wijaya  ", "Sari W."},
		{"Sari W.", "Sari W."},
		{"Sari Wijaya-Lestari", "Sari W."},
		{"Éviñ Ünïcode", "Éviñ Ü."},
		{"", ""},
		{"   ", ""},
	}
	for _, c := range cases {
		if got := redactName(c.in); got != c.want {
			t.Errorf("redactName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The therapist column is a first name only; the TV never shows a full staff
// name either.
func TestTVFirstName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Ayu Lestari", "Ayu"},
		{"Ayu", "Ayu"},
		{"  Ayu   Lestari ", "Ayu"},
		{"", ""},
	}
	for _, c := range cases {
		if got := tvFirstName(c.in); got != c.want {
			t.Errorf("tvFirstName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The band is the heart of the view: in_service is "now" (start order),
// checked_in beats confirmed regardless of its clock time, confirmed must be
// future, and the whole "next" strip is capped at three.
func TestTVQueueRowsBands(t *testing.T) {
	swapTVClock(t, tvQueueNow)
	rows := []domain.Booking{
		tvQueueBooking("Now Later", "Pijat Balinese", "Ayu Lestari", domain.BookingInService, domain.PlaceOnsite, 10, 15),
		tvQueueBooking("Now First", "Baby Massage", "Bidan Sari", domain.BookingInService, domain.PlaceOnsite, 10, 0),
		tvQueueBooking("Arrived One", "Pijat Balinese", "Ayu Lestari", domain.BookingCheckedIn, domain.PlaceOnsite, 9, 45),
		tvQueueBooking("Arrived Two", "Baby Massage", "Bidan Sari", domain.BookingCheckedIn, domain.PlaceOnsite, 9, 50),
		tvQueueBooking("Upcoming Late", "Pijat Balinese", "Ayu Lestari", domain.BookingConfirmed, domain.PlaceOnsite, 12, 0),
		tvQueueBooking("Upcoming Next", "Baby Massage", "Bidan Sari", domain.BookingConfirmed, domain.PlaceOnsite, 10, 30),
		tvQueueBooking("Upcoming Past", "Pijat Balinese", "Ayu Lestari", domain.BookingConfirmed, domain.PlaceOnsite, 8, 30),
		tvQueueBooking("Done Early", "Baby Massage", "Bidan Sari", domain.BookingCompleted, domain.PlaceOnsite, 9, 0),
	}
	swapTVQueue(t, rows, nil, nil)

	s := newFailTestServer(t)
	now, next, day, err := s.tvQueueRows(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("tvQueueRows: %v", err)
	}

	if got := tvNames(now); got != "Now F.|Now L." {
		t.Errorf("now = %q, want start order", got)
	}
	if got := tvNames(next); got != "Arrived O.|Arrived T.|Upcoming N." {
		t.Errorf("next = %q, want arrived first then the earliest future confirmed, capped at 3", got)
	}
	if got := tvNames(day); got != "Upcoming P.|Done E.|Arrived O.|Arrived T.|Now F.|Now L.|Upcoming N.|Upcoming L." {
		t.Errorf("day = %q, want all four allowed statuses by start time", got)
	}
	if now[0].TimeLabel != "10:00" {
		t.Errorf("TimeLabel = %q, want WIB 10:00", now[0].TimeLabel)
	}
	if now[0].ServiceName != "Baby Massage" || now[0].StaffFirst != "Bidan" {
		t.Errorf("now[0] = %+v, want service + therapist first name", now[0])
	}
	if now[0].NameRedacted != "Now F." {
		t.Errorf("now[0].NameRedacted = %q, want redacted", now[0].NameRedacted)
	}
	for _, r := range day {
		if r.StatusLabel == "" {
			t.Errorf("row %+v has no status label key", r)
		}
	}
}

// Every status the kiosk must never display, plus homecare, is enumerated here
// with a unique name prefix so a leak can only come from one fixture.
func TestTVQueueRowsExcludePrivateStatusesAndHomecare(t *testing.T) {
	swapTVClock(t, tvQueueNow)
	rows := []domain.Booking{
		tvQueueBooking("Zedcancelled One", "Pijat Balinese", "Ayu", domain.BookingCancelled, domain.PlaceOnsite, 10, 0),
		tvQueueBooking("Zednoshow Two", "Pijat Balinese", "Ayu", domain.BookingNoShow, domain.PlaceOnsite, 10, 15),
		tvQueueBooking("Zedexpired Three", "Pijat Balinese", "Ayu", domain.BookingExpired, domain.PlaceOnsite, 10, 30),
		tvQueueBooking("Zedpending Four", "Pijat Balinese", "Ayu", domain.BookingPendingPayment, domain.PlaceOnsite, 10, 45),
		tvQueueBooking("Zedhomecare Five", "Pijat Balinese", "Ayu", domain.BookingInService, domain.PlaceHomecare, 10, 0),
		tvQueueBooking("Zedhomecare Six", "Pijat Balinese", "Ayu", domain.BookingConfirmed, domain.PlaceHomecare, 11, 0),
		tvQueueBooking("Kept Onsite", "Pijat Balinese", "Ayu", domain.BookingConfirmed, domain.PlaceOnsite, 11, 30),
	}
	swapTVQueue(t, rows, nil, nil)

	s := newFailTestServer(t)
	now, next, day, err := s.tvQueueRows(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("tvQueueRows: %v", err)
	}
	all := append(append(append([]tvRow{}, now...), next...), day...)
	if len(day) != 1 || day[0].NameRedacted != "Kept O." {
		t.Fatalf("day = %+v, want only the onsite confirmed booking", day)
	}
	for _, band := range [][]tvRow{now, next} {
		for _, r := range band {
			if r.NameRedacted != "Kept O." {
				t.Fatalf("band = %+v, want only the onsite confirmed booking", band)
			}
		}
	}
	if len(now) != 0 || len(next) != 1 {
		t.Errorf("now/next rows = %d/%d, want 0/1", len(now), len(next))
	}
	rendered := tvNames(all)
	if strings.Contains(rendered, "Zed") {
		t.Errorf("excluded booking reached the TV rows: %q", rendered)
	}
}

// The builder must ask the repo for today in WIB, scoped to the token's
// branch — status/place narrowing happens client-side because the repo filter
// carries one status only.
func TestTVQueueRowsFilter(t *testing.T) {
	swapTVClock(t, tvQueueNow)
	var got repo.FilterParams
	swapTVQueue(t, nil, nil, &got)

	s := newFailTestServer(t)
	loc := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	if _, _, _, err := s.tvQueueRows(context.Background(), loc); err != nil {
		t.Fatalf("tvQueueRows: %v", err)
	}
	if got.LocationID != loc {
		t.Errorf("LocationID = %s, want %s", got.LocationID, loc)
	}
	if got.Date != "2026-11-21" {
		t.Errorf("Date = %q, want the WIB today", got.Date)
	}
	if got.Status != "" {
		t.Errorf("Status = %q, want empty (the repo filter is single-status)", got.Status)
	}
}

// tvHostileBooking is the worst-case row the portal could hold: full name,
// phone, email, baby/medical copy, address, payment state and price.
func tvHostileBooking() domain.Booking {
	b := tvQueueBooking("Sari Wijaya", "Pijat Balinese", "Ayu Lestari", domain.BookingInService, domain.PlaceOnsite, 10, 0)
	b.CustomerPhone = "081234567890"
	b.CustomerEmail = "sari@example.com"
	b.BabyName = "bayi sensitif"
	b.MomCondition = "asi eksklusif"
	b.Notes = "alergi lanolin"
	b.VisitAddress = "Jl. Rahasia No. 7"
	b.PaymentMethod = domain.PaymentMethodQRIS
	b.PaymentStatus = domain.PaymentPending
	b.PriceIDR = 350000
	return b
}

// The privacy test: the FULL page renders the redacted view-model and nothing
// else from the booking. This is the deliverable — not a convention.
func TestTVViewShowsNoPII(t *testing.T) {
	h := newRouteTestServer(t)
	tok := tvFixture()
	swapTVActive(t, &tok, nil, nil)
	swapTVClock(t, tvQueueNow)
	swapTVQueue(t, []domain.Booking{tvHostileBooking()}, nil, nil)

	for _, path := range []string{
		"/tv?lang=id&t=" + tvTestToken,
		"/tv/queue?lang=id&t=" + tvTestToken,
	} {
		rec := runRoute(h, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", path, rec.Code)
		}
		out := rec.Body.String()
		for _, want := range []string{"Sari W.", "Pijat Balinese", "Ayu", "Sedang dilayani"} {
			if !contains(out, want) {
				t.Errorf("%s: missing %q", path, want)
			}
		}
		low := strings.ToLower(out)
		for _, leak := range []string{
			"sari wijaya",      // the full name
			"081234567890",     // phone
			"sari@example.com", // email
			"bayi sensitif",    // baby/medical field
			"asi eksklusif",    // mom condition
			"alergi lanolin",   // free-text note
			"jl. rahasia",      // home address
			"pending",          // payment status
			"qris",             // payment method
			"350.000",          // formatted price (a bare digit run could collide with an asset hash)
			"lestari",          // the therapist's surname
		} {
			if strings.Contains(low, leak) {
				t.Errorf("%s: leaked %q into the kiosk HTML", path, leak)
			}
		}
	}
}

// Status filter holes are invisible in production but fatal here: render the
// page with one fixture per excluded status plus homecare and prove none of
// their names (unique "Zed" prefix) reaches the screen.
func TestTVViewHidesExcludedStatusesAndHomecare(t *testing.T) {
	h := newRouteTestServer(t)
	tok := tvFixture()
	swapTVActive(t, &tok, nil, nil)
	swapTVClock(t, tvQueueNow)
	swapTVQueue(t, []domain.Booking{
		tvQueueBooking("Zedcancelled One", "Pijat Balinese", "Ayu", domain.BookingCancelled, domain.PlaceOnsite, 10, 0),
		tvQueueBooking("Zednoshow Two", "Pijat Balinese", "Ayu", domain.BookingNoShow, domain.PlaceOnsite, 10, 15),
		tvQueueBooking("Zedexpired Three", "Pijat Balinese", "Ayu", domain.BookingExpired, domain.PlaceOnsite, 10, 30),
		tvQueueBooking("Zedpending Four", "Pijat Balinese", "Ayu", domain.BookingPendingPayment, domain.PlaceOnsite, 10, 45),
		tvQueueBooking("Zedhomecare Five", "Pijat Balinese", "Ayu", domain.BookingInService, domain.PlaceHomecare, 10, 0),
		tvQueueBooking("Kept Onsite", "Pijat Balinese", "Ayu", domain.BookingConfirmed, domain.PlaceOnsite, 11, 30),
	}, nil, nil)

	rec := runRoute(h, httptest.NewRequest(http.MethodGet, "/tv?lang=id&t="+tvTestToken, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	out := rec.Body.String()
	if !contains(out, "Kept O.") {
		t.Error("the allowed booking is missing from the page")
	}
	if strings.Contains(out, "Zed") {
		t.Error("an excluded status or a homecare booking reached the page")
	}
}

// Band placement is asserted on the rendered HTML, not just the view-model:
// the in_service row sits under "Sedang dilayani" and checked_in precedes the
// future confirmed rows under "Berikutnya"; completed appears only in the day
// list.
func TestTVViewBandOrdering(t *testing.T) {
	h := newRouteTestServer(t)
	tok := tvFixture()
	swapTVActive(t, &tok, nil, nil)
	swapTVClock(t, tvQueueNow)
	swapTVQueue(t, []domain.Booking{
		tvQueueBooking("Arrived One", "Pijat Balinese", "Ayu", domain.BookingCheckedIn, domain.PlaceOnsite, 9, 45),
		tvQueueBooking("Upcoming One", "Baby Massage", "Bidan", domain.BookingConfirmed, domain.PlaceOnsite, 11, 0),
		tvQueueBooking("Serving One", "Pijat Balinese", "Ayu", domain.BookingInService, domain.PlaceOnsite, 10, 0),
		tvQueueBooking("Done One", "Baby Massage", "Bidan", domain.BookingCompleted, domain.PlaceOnsite, 9, 0),
	}, nil, nil)

	rec := runRoute(h, httptest.NewRequest(http.MethodGet, "/tv?lang=id&t="+tvTestToken, nil))
	out := rec.Body.String()

	nowAt := strings.Index(out, "Sedang dilayani")
	nextAt := strings.Index(out, "Berikutnya")
	dayAt := strings.Index(out, "Jadwal hari ini")
	if nowAt < 0 || nextAt < 0 || dayAt < 0 || !(nowAt < nextAt && nextAt < dayAt) {
		t.Fatalf("band order wrong: now=%d next=%d day=%d", nowAt, nextAt, dayAt)
	}
	servingAt := strings.Index(out, "Serving O.")
	arrivedAt := strings.Index(out, "Arrived O.")
	upcomingAt := strings.Index(out, "Upcoming O.")
	doneAt := strings.Index(out, "Done O.")
	if !(nowAt < servingAt && servingAt < nextAt) {
		t.Errorf("in_service row must sit inside the now band (now=%d serving=%d next=%d)", nowAt, servingAt, nextAt)
	}
	if !(arrivedAt < upcomingAt && upcomingAt < dayAt) {
		t.Errorf("next band must order checked_in before future confirmed (arrived=%d upcoming=%d day=%d)", arrivedAt, upcomingAt, dayAt)
	}
	if !(dayAt < doneAt) {
		t.Errorf("completed row must appear in the day list (day=%d done=%d)", dayAt, doneAt)
	}
}

// A DB blip must not blank the wall: the poll answers 500 so htmx keeps the
// last good rows (responseHandling drops 4xx/5xx), and the full page still
// renders the kiosk shell with a retry notice.
func TestTVQueueFailureKeepsKioskUsable(t *testing.T) {
	h := newRouteTestServer(t)
	tok := tvFixture()
	swapTVActive(t, &tok, nil, nil)
	swapTVClock(t, tvQueueNow)
	swapTVQueue(t, nil, errors.New("db down"), nil)

	partial := runRoute(h, httptest.NewRequest(http.MethodGet, "/tv/queue?lang=id&t="+tvTestToken, nil))
	if partial.Code != http.StatusInternalServerError {
		t.Errorf("poll status = %d, want 500 (htmx keeps old rows)", partial.Code)
	}

	page := runRoute(h, httptest.NewRequest(http.MethodGet, "/tv?lang=id&t="+tvTestToken, nil))
	if page.Code != http.StatusOK {
		t.Fatalf("page status = %d, want 200", page.Code)
	}
	out := page.Body.String()
	if !contains(out, "tv-shell") {
		t.Error("error page lost the kiosk shell")
	}
	if !contains(out, "Antrean belum bisa dimuat") {
		t.Error("error page is missing the retry notice")
	}
}

// tvNames joins the redacted names of a band for order assertions.
func tvNames(rows []tvRow) string {
	names := make([]string, len(rows))
	for i, r := range rows {
		names[i] = r.NameRedacted
	}
	return strings.Join(names, "|")
}
