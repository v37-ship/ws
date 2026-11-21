package handlers

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"

	"github.com/google/uuid"
)

// queueCopy pins the literal labels the queue rows must show. Asserting
// i18n.T(lang, key) instead would pass even when the key is missing (T echoes
// the key back), which is exactly the failure these tests exist to catch.
var queueCopy = map[string]map[string]string{
	"en": {
		"ci":        "Mark Checked-In (In Lobby)",
		"sta":       "Start In-Service (In Room)",
		"fin":       "Mark Completed",
		"cfn":       "Mark as No-Show?",
		"cfc":       "Cancel this booking?",
		"cff":       "Mark this visit as completed?",
		"nos":       "Mark No-Show",
		"cx":        "Cancel Booking",
		"paid":      "Confirm payment",
		"reject":    "Reject claim",
		"rcpt":      "Receipt",
		"claimnote": "Confirmed from claims queue",
		"claimnone": "No payment claims today.",
		"todaynone": "No bookings scheduled for today yet.",
	},
	"id": {
		"ci":        "Tandai Check-In (Di Lobi)",
		"sta":       "Mulai Dilayani (Di Ruangan)",
		"fin":       "Tandai Selesai",
		"cfn":       "Tandai sebagai tidak hadir?",
		"cfc":       "Batalkan booking ini?",
		"cff":       "Tandai kunjungan ini selesai?",
		"nos":       "Tandai Tidak Hadir",
		"cx":        "Batalkan Booking",
		"paid":      "Konfirmasi pembayaran",
		"reject":    "Tolak klaim",
		"rcpt":      "Bukti",
		"claimnote": "Dikonfirmasi dari antrean klaim",
		"claimnone": "Belum ada klaim pembayaran hari ini.",
		"todaynone": "Belum ada booking terjadwal hari ini.",
	},
}

// queueBooking is the smallest row the queue partial needs. Every conditional
// block is driven by Status and PaymentStatus, so callers pick those.
func queueBooking(status domain.BookingStatus, pay domain.PaymentStatus) domain.Booking {
	start := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	return domain.Booking{
		ID: uuid.New(), CustomerName: "Bunda Ana", CustomerPhone: "081234567890",
		ServiceName: "Baby Massage", StaffName: "Bidan Ayu",
		StartAt: start, EndAt: start.Add(time.Hour), Status: status,
		PriceIDR: 150000, PaymentMethod: domain.PaymentMethodQRISStatic, PaymentStatus: pay,
	}
}

func renderQueue(t *testing.T, s *Server, data pageData) string {
	t.Helper()
	var buf bytes.Buffer
	if err := s.tpl.ExecuteTemplate(&buf, "portal_queue_rows.html", data); err != nil {
		t.Fatalf("execute queue rows: %v", err)
	}
	return buf.String()
}

// queueSweepData feeds the generic page/partial sweeps (missing-key and emoji
// scans): one confirmed and one in-service row in claims mode covers every
// conditional block of the row in a single render.
func queueSweepData(lang string) pageData {
	return pageData{
		"Lang": lang, "CSRF": "tok", "ClaimsMode": true,
		"TodayBookings": []domain.Booking{
			queueBooking(domain.BookingConfirmed, domain.PaymentPending),
			queueBooking(domain.BookingInService, domain.PaymentPaid),
		},
	}
}

// buttonTag returns the opening <button ...> tag that contains marker, so a
// test can pin an attribute (hx-confirm) to the button it belongs to instead of
// matching it anywhere in the row.
func buttonTag(out, marker string) string {
	i := strings.Index(out, marker)
	if i < 0 {
		return ""
	}
	start := strings.LastIndex(out[:i], "<button")
	if start < 0 {
		return ""
	}
	end := strings.Index(out[i:], ">")
	if end < 0 {
		return ""
	}
	return out[start : i+end+1]
}

// A queue row carries the same conditional status buttons as the detail page:
// a confirmed booking can be checked in, started, no-showed or cancelled from
// the row, with the destructive two keeping their confirm prompt.
func TestQueueRowStatusButtons(t *testing.T) {
	s := newFailTestServer(t)
	b := queueBooking(domain.BookingConfirmed, domain.PaymentPending)
	for _, lang := range []string{"en", "id"} {
		copy := queueCopy[lang]
		out := renderQueue(t, s, pageData{
			"Lang": lang, "CSRF": "tok", "TodayBookings": []domain.Booking{b},
		})
		id := b.ID.String()
		want := []string{
			`hx-post="/portal/bookings/` + id + `/status"`,
			`hx-swap="none"`,
			`name="csrf" value="tok"`,
			`value="checked_in"`,
			copy["ci"],
			`value="in_service"`,
			copy["sta"],
			`value="no_show"`,
			copy["nos"],
			`value="cancelled"`,
			copy["cx"],
			`hx-disabled-elt="this"`,
		}
		for _, w := range want {
			if !contains(out, w) {
				t.Errorf("queue row (%s): missing %q", lang, w)
			}
		}
		if got := buttonTag(out, `value="no_show"`); !contains(got, `hx-confirm="`+copy["cfn"]+`"`) {
			t.Errorf("queue row (%s): no-show button lost its confirm: %q", lang, got)
		}
		if got := buttonTag(out, `value="cancelled"`); !contains(got, `hx-confirm="`+copy["cfc"]+`"`) {
			t.Errorf("queue row (%s): cancel button lost its confirm: %q", lang, got)
		}
		// Mirrors the detail page: finish is not offered from confirmed.
		if contains(out, `value="completed"`) {
			t.Errorf("queue row (%s): confirmed booking must not offer finish", lang)
		}
	}
}

// Finish ends the visit, so the queue row asks first (design part 2). The
// in-service row offers finish but not check-in.
func TestQueueRowFinishNeedsConfirm(t *testing.T) {
	s := newFailTestServer(t)
	b := queueBooking(domain.BookingInService, domain.PaymentPending)
	for _, lang := range []string{"en", "id"} {
		copy := queueCopy[lang]
		out := renderQueue(t, s, pageData{
			"Lang": lang, "CSRF": "tok", "TodayBookings": []domain.Booking{b},
		})
		btn := buttonTag(out, `value="completed"`)
		if btn == "" {
			t.Fatalf("queue row (%s): in-service row missing finish button", lang)
		}
		if !contains(btn, `hx-confirm="`+copy["cff"]+`"`) {
			t.Errorf("queue row (%s): finish button must confirm first: %q", lang, btn)
		}
		if contains(out, `value="checked_in"`) {
			t.Errorf("queue row (%s): in-service booking must not offer check-in", lang)
		}
	}
}

// Claims mode is the money fast lane: receipt evidence plus inline confirm and
// reject. Confirm records qris_static with the auto-note (ruling 5) and cash
// never appears here — cash stays on the detail form.
func TestClaimsQueueRow(t *testing.T) {
	s := newFailTestServer(t)
	b := queueBooking(domain.BookingConfirmed, domain.PaymentPending)
	for _, lang := range []string{"en", "id"} {
		copy := queueCopy[lang]
		out := renderQueue(t, s, pageData{
			"Lang": lang, "CSRF": "tok", "ClaimsMode": true,
			"TodayBookings": []domain.Booking{b},
		})
		id := b.ID.String()
		want := []string{
			`href="/portal/bookings/` + id + `/receipt"`,
			copy["rcpt"],
			`hx-post="/portal/bookings/` + id + `/payment/confirm"`,
			`name="method" value="qris_static"`,
			`name="note" value="` + copy["claimnote"] + `"`,
			copy["paid"],
			`hx-post="/portal/bookings/` + id + `/payment/reject"`,
			copy["reject"],
		}
		for _, w := range want {
			if !contains(out, w) {
				t.Errorf("claims row (%s): missing %q", lang, w)
			}
		}
		for _, never := range []string{`value="cash"`, "<select", "Cash", "Tunai"} {
			if contains(out, never) {
				t.Errorf("claims row (%s): cash must not ride the fast lane, found %q", lang, never)
			}
		}
		// The auto-note must not leak an untranslated key.
		if contains(out, ">p.clm.") || contains(out, `"p.clm.`) {
			t.Errorf("claims row (%s): untranslated claims key in markup", lang)
		}
	}
}

// The payment block belongs to claims mode only: an ordinary queue row keeps
// its status buttons and stays out of the payment flow.
func TestClaimsQueueRowOnlyInClaimsMode(t *testing.T) {
	s := newFailTestServer(t)
	b := queueBooking(domain.BookingConfirmed, domain.PaymentPending)
	out := renderQueue(t, s, pageData{
		"Lang": "en", "CSRF": "tok", "TodayBookings": []domain.Booking{b},
	})
	for _, never := range []string{"/payment/confirm", "/payment/reject", "/receipt"} {
		if contains(out, never) {
			t.Errorf("plain queue row must not carry the claims controls, found %q", never)
		}
	}
}

// An empty claims filter must not claim "no bookings today" — the queue is
// filtered, so the copy says what is actually empty.
func TestClaimsQueueEmptyState(t *testing.T) {
	s := newFailTestServer(t)
	for _, lang := range []string{"en", "id"} {
		copy := queueCopy[lang]
		claims := renderQueue(t, s, pageData{"Lang": lang, "ClaimsMode": true})
		if !contains(claims, copy["claimnone"]) {
			t.Errorf("claims empty state (%s): want %q", lang, copy["claimnone"])
		}
		plain := renderQueue(t, s, pageData{"Lang": lang})
		if !contains(plain, copy["todaynone"]) {
			t.Errorf("plain empty state (%s): want %q", lang, copy["todaynone"])
		}
		if contains(plain, copy["claimnone"]) {
			t.Errorf("plain empty state (%s): must not use the claims copy", lang)
		}
	}
}

// The queue handler maps its query string to a list filter through one helper,
// so the initial dashboard render and the 30s poll cannot disagree. Claims mode
// reuses the bookings list's payment=claim predicate (pending + reported).
func TestClaimsQueueFilter(t *testing.T) {
	wib := time.FixedZone("WIB", 7*3600)
	now := time.Date(2026, 11, 21, 15, 30, 0, 0, wib)
	cases := []struct {
		query       string
		wantPayment string
		wantClaims  bool
	}{
		{"", "", false},
		{"?payment=claim", "claim", true},
		{"?payment=unpaid", "", false},
		{"?payment=paid", "", false},
		{"?payment=Claim", "", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/portal/queue"+c.query, nil)
		filter, claims := queueFilter(req, now)
		if filter.Payment != c.wantPayment {
			t.Errorf("queueFilter(%q).Payment = %q, want %q", c.query, filter.Payment, c.wantPayment)
		}
		if claims != c.wantClaims {
			t.Errorf("queueFilter(%q) claims = %v, want %v", c.query, claims, c.wantClaims)
		}
		if filter.Date != "2026-11-21" {
			t.Errorf("queueFilter(%q).Date = %q, want the WIB day", c.query, filter.Date)
		}
	}
}

// The queue handler is the wiring under test: ?payment=claim must reach the
// repo as the booked-list claim predicate and the rendered rows must be the
// claims fast lane. The repos are concrete pgx types, so the data call is
// swapped for a recorder instead of a database.
func TestClaimsQueueHandler(t *testing.T) {
	s := newFailTestServer(t)
	var gotFilter repo.FilterParams
	prev := queueBookings
	queueBookings = func(_ *Server, _ *http.Request, f repo.FilterParams) ([]domain.Booking, error) {
		gotFilter = f
		return []domain.Booking{queueBooking(domain.BookingConfirmed, domain.PaymentPending)}, nil
	}
	t.Cleanup(func() { queueBookings = prev })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/portal/queue?payment=claim&lang=en", nil)
	req.Header.Set("HX-Request", "true")
	s.PortalQueuePartial(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if gotFilter.Payment != "claim" {
		t.Errorf("queue filter Payment = %q, want claim (the claimed-unpaid predicate)", gotFilter.Payment)
	}
	out := rec.Body.String()
	for _, want := range []string{
		`name="method" value="qris_static"`,
		`name="note" value="` + queueCopy["en"]["claimnote"] + `"`,
		"/payment/reject",
		"/receipt",
	} {
		if !contains(out, want) {
			t.Errorf("claims queue response missing %q", want)
		}
	}
}

// Without the filter the same handler loads the whole day and stays out of the
// payment flow.
func TestQueueHandlerPlain(t *testing.T) {
	s := newFailTestServer(t)
	var gotFilter repo.FilterParams
	prev := queueBookings
	queueBookings = func(_ *Server, _ *http.Request, f repo.FilterParams) ([]domain.Booking, error) {
		gotFilter = f
		return []domain.Booking{queueBooking(domain.BookingConfirmed, domain.PaymentPending)}, nil
	}
	t.Cleanup(func() { queueBookings = prev })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/portal/queue", nil)
	req.Header.Set("HX-Request", "true")
	s.PortalQueuePartial(rec, req)

	if gotFilter.Payment != "" {
		t.Errorf("plain queue filter Payment = %q, want no payment filter", gotFilter.Payment)
	}
	if gotFilter.Date == "" {
		t.Error("plain queue filter Date is empty, want the WIB day")
	}
	out := rec.Body.String()
	if contains(out, "/payment/confirm") || contains(out, "/receipt") {
		t.Error("plain queue response must not carry the claims controls")
	}
	if contains(out, `value="checked_in"`) == false {
		t.Error("plain queue response lost the status buttons")
	}
}

// The dashboard's claims stat opens the filtered queue, and the poll wrapper
// must carry the filter — otherwise the next 30s sweep silently swaps the
// claims fast lane back to the full day.
func TestClaimsQueueDashboardLink(t *testing.T) {
	s := newFailTestServer(t)
	contact := pageData{
		"WhatsApp": "62800", "Email": "a@b.c", "Address": "addr", "Hours": "hours",
		"Maps": "https://maps.example/", "Instagram": "https://instagram.example/",
		"TikTok": "https://tiktok.example/", "Facebook": "https://facebook.example/",
	}
	render := func(claims bool) string {
		t.Helper()
		var buf bytes.Buffer
		if err := s.tpl.ExecuteTemplate(&buf, "portal_dashboard.html", pageData{
			"Lang": "en", "Contact": contact, "ClaimsMode": claims,
			"TodayStr": "Sat 21 Nov 2026", "Stats": PortalStats{},
		}); err != nil {
			t.Fatalf("execute dashboard: %v", err)
		}
		return buf.String()
	}

	claims := render(true)
	if !contains(claims, `href="/portal?payment=claim"`) {
		t.Error("dashboard: claims stat must link to the filtered queue")
	}
	if !contains(claims, `hx-get="/portal/queue?payment=claim"`) {
		t.Error("dashboard: queue poll must keep the claims filter")
	}
	if !contains(claims, `<span class="badge">Claims</span>`) {
		t.Error("dashboard: filtered queue must say it is the claims view")
	}

	plain := render(false)
	if contains(plain, `hx-get="/portal/queue?payment=claim"`) {
		t.Error("dashboard: plain queue poll must not request claims rows")
	}
	if !contains(plain, `hx-get="/portal/queue"`) {
		t.Error("dashboard: plain queue poll missing")
	}
	if contains(plain, `<span class="badge">Claims</span>`) {
		t.Error("dashboard: plain queue must not claim to be the claims view")
	}
}
