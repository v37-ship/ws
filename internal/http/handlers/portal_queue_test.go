package handlers

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"
	"spa-booking/internal/service"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// queueCopy pins the literal labels the queue rows must show. Asserting
// i18n.T(lang, key) instead would pass even when the key is missing (T echoes
// the key back), which is exactly the failure these tests exist to catch.
var queueCopy = map[string]map[string]string{
	"en": {
		"ci":          "Mark Checked-In (In Lobby)",
		"sta":         "Start In-Service (In Room)",
		"fin":         "Mark Completed",
		"cfn":         "Mark as No-Show?",
		"cfc":         "Cancel this booking?",
		"cff":         "Mark this visit as completed?",
		"nos":         "Mark No-Show",
		"cx":          "Cancel Booking",
		"paid":        "Confirm payment",
		"reject":      "Reject claim",
		"rcpt":        "Receipt",
		"claimnote":   "Confirmed from claims queue",
		"claimnone":   "No payment claims waiting.",
		"receiptnone": "No receipt uploaded",
		"todaynone":   "No bookings scheduled for today yet.",
		"undo":        "Undo last status change",
	},
	"id": {
		"ci":          "Tandai Check-In (Di Lobi)",
		"sta":         "Mulai Dilayani (Di Ruangan)",
		"fin":         "Tandai Selesai",
		"cfn":         "Tandai sebagai tidak hadir?",
		"cfc":         "Batalkan booking ini?",
		"cff":         "Tandai kunjungan ini selesai?",
		"nos":         "Tandai Tidak Hadir",
		"cx":          "Batalkan Booking",
		"paid":        "Konfirmasi pembayaran",
		"reject":      "Tolak klaim",
		"rcpt":        "Bukti",
		"claimnote":   "Dikonfirmasi dari antrean klaim",
		"claimnone":   "Belum ada klaim pembayaran.",
		"receiptnone": "Belum ada bukti transfer",
		"todaynone":   "Belum ada booking terjadwal hari ini.",
		"undo":        "Kembalikan status terakhir",
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
		HasReceipt: true,
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

// A claim can arrive without an uploaded transfer proof. The row then shows an
// honest muted note instead of a receipt link that answers 404.
func TestClaimsQueueReceiptAffordance(t *testing.T) {
	s := newFailTestServer(t)
	for _, lang := range []string{"en", "id"} {
		copy := queueCopy[lang]
		with := queueBooking(domain.BookingConfirmed, domain.PaymentPending)
		out := renderQueue(t, s, pageData{
			"Lang": lang, "CSRF": "tok", "ClaimsMode": true,
			"TodayBookings": []domain.Booking{with},
		})
		if !contains(out, `href="/portal/bookings/`+with.ID.String()+`/receipt"`) {
			t.Errorf("claims row (%s): receipt link missing when a receipt exists", lang)
		}
		if contains(out, copy["receiptnone"]) {
			t.Errorf("claims row (%s): must not say the receipt is missing when it exists", lang)
		}

		without := queueBooking(domain.BookingConfirmed, domain.PaymentPending)
		without.HasReceipt = false
		out = renderQueue(t, s, pageData{
			"Lang": lang, "CSRF": "tok", "ClaimsMode": true,
			"TodayBookings": []domain.Booking{without},
		})
		if !contains(out, copy["receiptnone"]) {
			t.Errorf("claims row (%s): want the no-receipt note %q", lang, copy["receiptnone"])
		}
		if contains(out, `/receipt"`) {
			t.Errorf("claims row (%s): no receipt link may render without a receipt", lang)
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
	// Today's queue already has the date in its heading; only the all-time
	// claims list repeats it per row.
	if contains(out, "21 Nov 2026") {
		t.Error("plain queue row must not carry the claims-mode date line")
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
// is the bookings list's payment=claim predicate with NO day scope: the Claims
// stat counts every outstanding claim (repo.PaymentRepo.Counts), so a
// day-scoped queue would hide rows the number promised.
func TestClaimsQueueFilter(t *testing.T) {
	wib := time.FixedZone("WIB", 7*3600)
	now := time.Date(2026, 11, 21, 15, 30, 0, 0, wib)
	cases := []struct {
		query       string
		wantPayment string
		wantDate    string
		wantClaims  bool
	}{
		{"", "", "2026-11-21", false},
		{"?payment=claim", "claim", "", true},
		{"?payment=unpaid", "", "2026-11-21", false},
		{"?payment=paid", "", "2026-11-21", false},
		{"?payment=Claim", "", "2026-11-21", false},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/portal/queue"+c.query, nil)
		filter, claims := queueFilter(req, now)
		if filter.Payment != c.wantPayment {
			t.Errorf("queueFilter(%q).Payment = %q, want %q", c.query, filter.Payment, c.wantPayment)
		}
		if filter.Date != c.wantDate {
			t.Errorf("queueFilter(%q).Date = %q, want %q", c.query, filter.Date, c.wantDate)
		}
		if claims != c.wantClaims {
			t.Errorf("queueFilter(%q) claims = %v, want %v", c.query, claims, c.wantClaims)
		}
	}
	// Claims mode adds the claim predicate and nothing else, so it cannot drift
	// from the all-time count the dashboard shows.
	filter, _ := queueFilter(httptest.NewRequest(http.MethodGet, "/portal/queue?payment=claim", nil), now)
	if filter != (repo.FilterParams{Payment: "claim"}) {
		t.Errorf("claims filter = %+v, want only the claim predicate", filter)
	}
}

// The queue handler is the wiring under test: ?payment=claim must reach the
// repo as the bookings-list claim predicate with no day scope, and the rendered
// rows must be the claims fast lane. A claim from yesterday has to show up
// where staff are looking, with its date visible so it is not mistaken for
// today's. The repos are concrete pgx types, so the data call is swapped for a
// recorder instead of a database.
func TestClaimsQueueHandler(t *testing.T) {
	s := newFailTestServer(t)
	var gotFilter repo.FilterParams
	prev := queueBookings
	queueBookings = func(_ *Server, _ *http.Request, f repo.FilterParams) ([]domain.Booking, error) {
		gotFilter = f
		old := queueBooking(domain.BookingConfirmed, domain.PaymentPending)
		old.StartAt = time.Date(2026, 11, 20, 3, 0, 0, 0, time.UTC) // 10:00 WIB, not today
		old.EndAt = old.StartAt.Add(time.Hour)
		return []domain.Booking{old}, nil
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
	if gotFilter.Date != "" {
		t.Errorf("claims queue filter Date = %q, want no day scope (all unresolved claims)", gotFilter.Date)
	}
	out := rec.Body.String()
	for _, want := range []string{
		`name="method" value="qris_static"`,
		`name="note" value="` + queueCopy["en"]["claimnote"] + `"`,
		"/payment/reject",
		"/receipt",
		"20 Nov 2026", // the row carries its own date outside today's queue
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

// A queue poll that cannot reach the database must not answer a bare 500: the
// app's HTMX config drops non-2xx bodies, so the queue would silently go stale
// on screen. It takes the staffFail contract instead — error toast, Reswap none
// (the last good rows stay visible) and no body.
func TestQueuePartialFailureToasts(t *testing.T) {
	s := newFailTestServer(t)
	prev := queueBookings
	queueBookings = func(_ *Server, _ *http.Request, _ repo.FilterParams) ([]domain.Booking, error) {
		return nil, fmt.Errorf("db down")
	}
	t.Cleanup(func() { queueBookings = prev })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/portal/queue?lang=en", nil)
	req.Header.Set("HX-Request", "true")
	s.PortalQueuePartial(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 preserved", rec.Code)
	}
	if body := rec.Body.String(); body != "" {
		t.Errorf("HTMX failure must not write a body, got %q", body)
	}
	if got := rec.Header().Get("HX-Reswap"); got != "none" {
		t.Errorf("HX-Reswap = %q, want none (keep the last good rows)", got)
	}
	kind, msg := decodeToast(t, rec)
	if kind != "error" {
		t.Errorf("toast kind = %q, want error", kind)
	}
	if msg != failQueue {
		t.Errorf("toast message = %q, want %q", msg, failQueue)
	}
}

// undoStatusTestServer wires the undo data seam to a fixed outcome, exactly
// like queueBookings pins the queue's data call (the repos are concrete pgx
// types, so there is no interface to fake).
func undoStatusTestServer(t *testing.T, from, prev domain.BookingStatus, err error) *Server {
	t.Helper()
	s := newFailTestServer(t)
	orig := undoStatus
	undoStatus = func(_ *Server, _ *http.Request, _ uuid.UUID, _ *uuid.UUID) (domain.BookingStatus, domain.BookingStatus, error) {
		return prev, from, err
	}
	t.Cleanup(func() { undoStatus = orig })
	return s
}

func undoRequest(t *testing.T, id uuid.UUID, lang string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost,
		"/portal/bookings/"+id.String()+"/status/undo?lang="+lang,
		strings.NewReader("csrf=tok"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.AddCookie(&http.Cookie{Name: "csrf", Value: "tok"})
	// Handlers read the booking id from chi's route context; a direct handler
	// test has to supply it (no router in the path).
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id.String())
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// The queue row always carries the undo control, whatever the status: it is the
// one-tap safety net for a mis-click made in the queue itself (ruling 4). A
// refusal is explained by the toast, so the control never hides.
func TestQueueRowUndoControl(t *testing.T) {
	s := newFailTestServer(t)
	statuses := []domain.BookingStatus{
		domain.BookingConfirmed, domain.BookingInService,
		domain.BookingCompleted, domain.BookingCancelled,
	}
	for _, lang := range []string{"en", "id"} {
		copy := queueCopy[lang]
		for _, st := range statuses {
			b := queueBooking(st, domain.PaymentPaid)
			out := renderQueue(t, s, pageData{
				"Lang": lang, "CSRF": "tok", "TodayBookings": []domain.Booking{b},
			})
			if !contains(out, `hx-post="/portal/bookings/`+b.ID.String()+`/status/undo"`) {
				t.Errorf("queue row (%s, %s): missing undo control", lang, st)
			}
			if !contains(out, copy["undo"]) {
				t.Errorf("queue row (%s, %s): undo control lost its label %q", lang, st, copy["undo"])
			}
		}
	}
}

// A mismatched chain (someone moved the booking since) must answer an error
// toast and NO refresh: the status did not change, so the row/page must not be
// presented as if it had. staffFail keeps the 422 and skips the body.
func TestUndoStatusHandlerRefusal(t *testing.T) {
	id := uuid.New()
	s := undoStatusTestServer(t, "", "", fmt.Errorf("%w: no undoable status change", service.ErrValidation))

	rec := httptest.NewRecorder()
	s.PortalUndoBookingStatus(rec, undoRequest(t, id, "en"))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if body := rec.Body.String(); body != "" {
		t.Errorf("HTMX failure must not write a body, got %q", body)
	}
	if got := rec.Header().Get("HX-Refresh"); got != "" {
		t.Errorf("refused undo must not refresh (nothing changed), got HX-Refresh %q", got)
	}
	kind, msg := decodeToast(t, rec)
	if kind != "error" {
		t.Errorf("toast kind = %q, want error", kind)
	}
	if !strings.Contains(msg, "no undoable status change") {
		t.Errorf("toast message = %q, want the refusal reason", msg)
	}
}

// A successful undo refreshes (badge and action panel swap) and says what it
// restored. Undoing a cancellation appends the quiet-undo guidance instead of
// silently resending a confirmation (ruling 3).
func TestUndoStatusHandlerSuccess(t *testing.T) {
	cases := []struct {
		name    string
		from    domain.BookingStatus
		prev    domain.BookingStatus
		wantWav bool
	}{
		{name: "un-finish", from: domain.BookingCompleted, prev: domain.BookingInService},
		{name: "un-cancel", from: domain.BookingCancelled, prev: domain.BookingConfirmed, wantWav: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := undoStatusTestServer(t, c.from, c.prev, nil)
			rec := httptest.NewRecorder()
			s.PortalUndoBookingStatus(rec, undoRequest(t, uuid.New(), "en"))

			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204", rec.Code)
			}
			if got := rec.Header().Get("HX-Refresh"); got != "true" {
				t.Errorf("HX-Refresh = %q, want true", got)
			}
			kind, msg := decodeToast(t, rec)
			if kind != "success" {
				t.Errorf("toast kind = %q, want success", kind)
			}
			if !strings.Contains(msg, "Status restored to "+c.prev.Display()) {
				t.Errorf("toast message = %q, want the restored status", msg)
			}
			warn := strings.Contains(msg, "already notified of the cancellation")
			if warn != c.wantWav {
				t.Errorf("toast guidance present = %v, want %v (%q)", warn, c.wantWav, msg)
			}
		})
	}
}

// The detail page is where an admin or CS notices the mistake later: it gets
// the same undo control beside the status panel, and its Finish button now asks
// first like the queue row already did (design part 2).
func TestBookingDetailUndoControl(t *testing.T) {
	s := newFailTestServer(t)
	b := queueBooking(domain.BookingInService, domain.PaymentPaid)
	for _, lang := range []string{"en", "id"} {
		copy := queueCopy[lang]
		var buf bytes.Buffer
		data := pageData{
			"Lang": lang, "CSRF": "tok", "Booking": &b, "Refundable": int64(0),
			"Contact": pageData{
				"WhatsApp": "62800", "Email": "a@b.c", "Address": "addr", "Hours": "hours",
				"Maps": "https://maps.example/", "Instagram": "https://instagram.example/",
				"TikTok": "https://tiktok.example/", "Facebook": "https://facebook.example/",
			},
		}
		if err := s.tpl.ExecuteTemplate(&buf, "portal_booking_detail.html", data); err != nil {
			t.Fatalf("execute booking detail (%s): %v", lang, err)
		}
		out := buf.String()
		if !contains(out, `hx-post="/portal/bookings/`+b.ID.String()+`/status/undo"`) {
			t.Errorf("detail (%s): missing undo control", lang)
		}
		if !contains(out, copy["undo"]) {
			t.Errorf("detail (%s): undo control lost its label %q", lang, copy["undo"])
		}
		finish := buttonTag(out, `value="completed"`)
		if finish == "" {
			t.Fatalf("detail (%s): in-service booking missing finish button", lang)
		}
		if !contains(finish, `hx-confirm="`+copy["cff"]+`"`) {
			t.Errorf("detail (%s): finish button must confirm first: %q", lang, finish)
		}
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
	// All-time claims are not "today's queue": the heading must not promise a
	// day the rows do not have.
	if !contains(claims, `<h2>Claims to review</h2>`) {
		t.Error("dashboard: filtered queue must be headed as the claims view")
	}
	if contains(claims, "Sat 21 Nov 2026") {
		t.Error("dashboard: claims queue must not carry the today heading")
	}

	plain := render(false)
	if contains(plain, `hx-get="/portal/queue?payment=claim"`) {
		t.Error("dashboard: plain queue poll must not request claims rows")
	}
	if !contains(plain, `hx-get="/portal/queue"`) {
		t.Error("dashboard: plain queue poll missing")
	}
	if !contains(plain, `<h2>Today&#39;s Live Queue (Sat 21 Nov 2026)</h2>`) {
		t.Error("dashboard: plain queue must keep the today heading")
	}
}
