package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"

	"github.com/google/uuid"
)

// tvPairKey is a realistic pending key: 32 random bytes hex, the shape
// service.NewAccessToken mints. It is also the device secret's plaintext (the
// portal hashes this same key into the device row, so the kiosk can claim its
// credential without any secret ever travelling to the counter).
const tvPairKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

// tvPairCodeRe pins the format ruling: six digits, zero-padded, nothing else.
var tvPairCodeRe = regexp.MustCompile(`^[0-9]{6}$`)

func tvPairingFixture() domain.TVPairing {
	return domain.TVPairing{
		ID:         uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		PendingKey: tvPairKey,
		Code:       "482913",
		ExpiresAt:  time.Now().Add(5 * time.Minute),
	}
}

// swapTVPairingCreate pins the pairing-creation seam and records every row the
// screen asked the repo to persist (the repos are concrete pgx types, so the
// tests substitute the call exactly like tvTokenActive does).
func swapTVPairingCreate(t *testing.T, saved *[]domain.TVPairing, err error) {
	t.Helper()
	prev := tvPairingCreate
	tvPairingCreate = func(_ *Server, _ *http.Request, p *domain.TVPairing) error {
		if saved != nil {
			*saved = append(*saved, *p)
		}
		return err
	}
	t.Cleanup(func() { tvPairingCreate = prev })
}

// swapTVPairingByKey pins the pending-key lookup seam the state poll uses.
func swapTVPairingByKey(t *testing.T, p *domain.TVPairing, err error, gotKey *string) {
	t.Helper()
	prev := tvPairingByKey
	tvPairingByKey = func(_ *Server, _ *http.Request, key string) (*domain.TVPairing, error) {
		if gotKey != nil {
			*gotKey = key
		}
		return p, err
	}
	t.Cleanup(func() { tvPairingByKey = prev })
}

// tvCookie finds one Set-Cookie by name; nil means the response did not touch
// that cookie at all (which some assertions need to distinguish from a clear).
func tvCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// The pairing screen is the no-credential face of /tv: it must show THIS
// screen's six-digit code, carry the pending-key cookie, and — the privacy
// contract — render not one byte of booking data even when the branch's queue
// holds the worst-case row. It must not even ask the queue seam.
func TestTVPairingScreenShowsCodeAndNoBookingData(t *testing.T) {
	h := newRouteTestServer(t)
	var created []domain.TVPairing
	swapTVPairingCreate(t, &created, nil)
	swapTVClock(t, tvQueueNow)

	queueCalls := 0
	prevQueue := tvQueueBookings
	tvQueueBookings = func(_ *Server, _ context.Context, _ repo.FilterParams) ([]domain.Booking, error) {
		queueCalls++
		return []domain.Booking{tvHostileBooking()}, nil
	}
	t.Cleanup(func() { tvQueueBookings = prevQueue })

	rec := runRoute(h, httptest.NewRequest(http.MethodGet, "/tv?lang=id", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(created) != 1 {
		t.Fatalf("pairing rows created = %d, want 1", len(created))
	}
	p := created[0]
	if !tvPairCodeRe.MatchString(p.Code) {
		t.Errorf("code = %q, want six digits", p.Code)
	}
	if p.PendingKey == "" {
		t.Error("pairing has no pending key")
	}
	if want := tvQueueNow.Add(5 * time.Minute); !p.ExpiresAt.Equal(want) {
		t.Errorf("expires_at = %s, want %s (5-minute TTL)", p.ExpiresAt, want)
	}
	out := rec.Body.String()
	if !contains(out, p.Code) {
		t.Error("the screen does not show its own code")
	}
	for _, want := range []string{
		"Hubungkan layar ini",     // tv.pr.title
		"Menunggu konfirmasi",     // tv.pr.wait
		`hx-get="/tv/pair-state"`, // the state poll
		`hx-trigger="every 4s"`,   // its clock
		`id="tv-pair-state"`,      // its swap target
	} {
		if !contains(out, want) {
			t.Errorf("pairing screen missing %q", want)
		}
	}
	if contains(out, "Display not available") || contains(out, "Tampilan tidak tersedia") {
		t.Error("bare /tv must start a pairing, not render the invalid page")
	}

	// The pending key rides an HttpOnly, 15-minute, Lax cookie.
	c := tvCookie(rec, "tv_pending")
	if c == nil {
		t.Fatal("no tv_pending cookie was set")
	}
	if c.Value != p.PendingKey {
		t.Errorf("tv_pending = %q, want the pairing's pending key", c.Value)
	}
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Errorf("tv_pending flags = HttpOnly:%v SameSite:%v Path:%q, want true/Lax//", c.HttpOnly, c.SameSite, c.Path)
	}
	if c.MaxAge != 15*60 {
		t.Errorf("tv_pending MaxAge = %d, want 900 (15m)", c.MaxAge)
	}

	if queueCalls != 0 {
		t.Errorf("the pairing screen read the queue %d times, want 0", queueCalls)
	}
	if strings.Contains(out, p.PendingKey) {
		t.Error("the pending key must live in the HttpOnly cookie only, never in the HTML")
	}
	low := strings.ToLower(out)
	for _, leak := range []string{
		"sari wijaya", "081234567890", "sari@example.com", "bayi sensitif",
		"asi eksklusif", "alergi lanolin", "jl. rahasia", "pending", "qris",
		"350.000", "lestari",
	} {
		if strings.Contains(low, leak) {
			t.Errorf("pairing screen leaked %q", leak)
		}
	}
}

// The pairing page's 4s poll inherits the shell the screen was opened with, so
// a queue-only bookmark survives a re-pair. Rotate is the default, so it
// appends nothing and a retired split bookmark lands on rotate.
func TestTVPairingStartCarriesLayout(t *testing.T) {
	for _, tc := range []struct{ name, query, want string }{
		{"default (rotate)", "", `hx-get="/tv/pair-state"`},
		{"explicit rotate", "&layout=rotate", `hx-get="/tv/pair-state"`},
		{"legacy split lands on rotate", "&layout=split", `hx-get="/tv/pair-state"`},
		{"queue only", "&layout=queue", `hx-get="/tv/pair-state?layout=queue"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRouteTestServer(t)
			swapTVPairingCreate(t, nil, nil)
			swapTVClock(t, tvQueueNow)

			rec := runRoute(h, httptest.NewRequest(http.MethodGet, "/tv?lang=id"+tc.query, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			out := rec.Body.String()
			if !contains(out, tc.want) {
				t.Errorf("pairing page missing %q", tc.want)
			}
			for _, unwanted := range []string{
				`hx-get="/tv/pair-state?layout=rotate"`,
				`hx-get="/tv/pair-state?layout=split"`,
			} {
				if contains(out, unwanted) {
					t.Errorf("pairing page must not carry the retired/extra param %q", unwanted)
				}
			}
		})
	}
}

// The expired fragment's "start over" link is the non-HTMX fallback; it must
// carry the queue-only shell through the re-pair like the poll does.
func TestTVPairStateExpiredLinkCarriesQueueLayout(t *testing.T) {
	h := newRouteTestServer(t)
	swapTVPairingByKey(t, nil, repo.ErrNotFound, nil)
	req := httptest.NewRequest(http.MethodGet, "/tv/pair-state?lang=id&layout=queue", nil)
	req.AddCookie(&http.Cookie{Name: "tv_pending", Value: tvPairKey})
	out := runRoute(h, req).Body.String()
	if !contains(out, `href="/tv?layout=queue"`) {
		t.Errorf("expired fragment missing the queue-only start-over link")
	}
}

// Ruling #10: creating pairing rows is an unauthenticated write, so the GET is
// rate-limited per IP at the login scale (6/min). The 7th start in a minute is
// refused before it reaches the repo.
func TestTVPairingStartIsRateLimited(t *testing.T) {
	h := newRouteTestServer(t)
	var created []domain.TVPairing
	swapTVPairingCreate(t, &created, nil)

	for i := 1; i <= 6; i++ {
		rec := runRoute(h, httptest.NewRequest(http.MethodGet, "/tv?lang=id", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("start %d: status = %d, want 200", i, rec.Code)
		}
	}
	seventh := runRoute(h, httptest.NewRequest(http.MethodGet, "/tv?lang=id", nil))
	if seventh.Code != http.StatusTooManyRequests {
		t.Errorf("7th start status = %d, want 429", seventh.Code)
	}
	if len(created) != 6 {
		t.Errorf("pairings created = %d, want 6 (the refused start must not write)", len(created))
	}
}

// The poll's pending state is a tiny fragment: no redirect, no device cookie,
// and it echoes the value the screen shows (wait, do not re-pair).
func TestTVPairStatePending(t *testing.T) {
	t.Run("fresh code", func(t *testing.T) {
		h := newRouteTestServer(t)
		p := tvPairingFixture()
		var gotKey string
		swapTVPairingByKey(t, &p, nil, &gotKey)

		req := httptest.NewRequest(http.MethodGet, "/tv/pair-state?lang=id", nil)
		req.AddCookie(&http.Cookie{Name: "tv_pending", Value: tvPairKey})
		rec := runRoute(h, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		if gotKey != tvPairKey {
			t.Errorf("lookup key = %q, want the cookie's pending key", gotKey)
		}
		assertTVPairPending(t, rec)
	})

	// The counter has consumed the code but has not linked the device yet (the
	// milliseconds between Consume and LinkDevice): that is still pending. If
	// this answered "expired", the poll would clear the pending cookie and
	// orphan the device the counter just minted.
	t.Run("claimed but not yet linked", func(t *testing.T) {
		h := newRouteTestServer(t)
		p := tvPairingFixture()
		p.ConsumedAt = time.Now()
		swapTVPairingByKey(t, &p, nil, nil)

		req := httptest.NewRequest(http.MethodGet, "/tv/pair-state?lang=id", nil)
		req.AddCookie(&http.Cookie{Name: "tv_pending", Value: tvPairKey})
		assertTVPairPending(t, runRoute(h, req))
	})
}

func assertTVPairPending(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	out := rec.Body.String()
	if !contains(out, "Menunggu konfirmasi") {
		t.Errorf("pending fragment = %q, want the waiting copy", out)
	}
	if contains(out, "kedaluwarsa") {
		t.Error("a live code must not be reported as expired")
	}
	if got := rec.Header().Get("HX-Redirect"); got != "" {
		t.Errorf("pending must not redirect, got HX-Redirect %q", got)
	}
	if c := tvCookie(rec, "tv_device"); c != nil {
		t.Errorf("pending must not set a device cookie, got %q", c.Value)
	}
	if c := tvCookie(rec, "tv_pending"); c != nil && c.MaxAge < 0 {
		t.Error("pending must not clear the pending cookie")
	}
}

// Paired is the handshake's payoff: the kiosk receives the long-lived device
// cookie (id + its own pending key, the secret the portal hashed) and is sent
// back to /tv, where the unified chain takes over. The fixture is consumed —
// that is what a real paired row looks like — so this also pins that "paired"
// wins over the liveness gate.
func TestTVPairStatePairedSetsDeviceCookie(t *testing.T) {
	h := newRouteTestServer(t)
	p := tvPairingFixture()
	p.ConsumedAt = time.Now()
	dev := uuid.MustParse("44444444-4444-4444-4444-444444444444")
	p.DeviceID = &dev
	swapTVPairingByKey(t, &p, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/tv/pair-state?lang=id", nil)
	req.AddCookie(&http.Cookie{Name: "tv_pending", Value: tvPairKey})
	rec := runRoute(h, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (redirect without a swap)", rec.Code)
	}
	if got := rec.Header().Get("HX-Redirect"); got != "/tv" {
		t.Errorf("HX-Redirect = %q, want /tv", got)
	}
	if body := rec.Body.String(); body != "" {
		t.Errorf("paired response must not write a body, got %q", body)
	}
	c := tvCookie(rec, "tv_device")
	if c == nil {
		t.Fatal("paired response did not set tv_device")
	}
	if want := domain.TVDeviceCookie(dev, tvPairKey); c.Value != want {
		t.Errorf("tv_device = %q, want %q", c.Value, want)
	}
	if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Errorf("tv_device flags = HttpOnly:%v SameSite:%v Path:%q, want true/Lax//", c.HttpOnly, c.SameSite, c.Path)
	}
	if c.MaxAge != 30*24*60*60 {
		t.Errorf("tv_device MaxAge = %d, want 30 days", c.MaxAge)
	}
	// The handshake is over: the device cookie replaces the pending key, and
	// the stale tv_pending (15m) must not ride along as a second credential.
	if c := tvCookie(rec, "tv_pending"); c == nil {
		t.Fatal("paired response must clear the stale tv_pending cookie")
	} else if c.Value != "" || c.MaxAge >= 0 {
		t.Errorf("tv_pending = (%q, MaxAge %d), want cleared", c.Value, c.MaxAge)
	}
}

// The redirect target follows the shell the pairing page was opened with.
// Rotate is the default, so it appends nothing; only a queue-only bookmark is
// carried through — and a retired split bookmark lands on rotate, not an error.
func TestTVPairStatePairedCarriesLayout(t *testing.T) {
	for _, tc := range []struct{ name, query, want string }{
		{"default shell (rotate)", "", "/tv"},
		{"explicit rotate shell", "?layout=rotate", "/tv"},
		{"legacy split lands on rotate", "?layout=split", "/tv"},
		{"queue-only shell", "?layout=queue", "/tv?layout=queue"},
		{"unknown layout stays rotate", "?layout=ROTATE", "/tv"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRouteTestServer(t)
			p := tvPairingFixture()
			p.ConsumedAt = time.Now()
			dev := uuid.MustParse("44444444-4444-4444-4444-444444444444")
			p.DeviceID = &dev
			swapTVPairingByKey(t, &p, nil, nil)

			req := httptest.NewRequest(http.MethodGet, "/tv/pair-state"+tc.query, nil)
			req.AddCookie(&http.Cookie{Name: "tv_pending", Value: tvPairKey})
			rec := runRoute(h, req)

			if got := rec.Header().Get("HX-Redirect"); got != tc.want {
				t.Errorf("HX-Redirect = %q, want %q", got, tc.want)
			}
		})
	}
}

// Dead ends all answer expired: a non-HTMX hit gets the notice fragment (the
// only path that renders it) and the stale cookie is cleared either way; the
// live 4s poll instead self-rearms — see TestTVPairStateExpiredRearms.
func TestTVPairStateExpired(t *testing.T) {
	t.Run("unknown pending key", func(t *testing.T) {
		h := newRouteTestServer(t)
		swapTVPairingByKey(t, nil, repo.ErrNotFound, nil)
		req := httptest.NewRequest(http.MethodGet, "/tv/pair-state?lang=id", nil)
		req.AddCookie(&http.Cookie{Name: "tv_pending", Value: tvPairKey})
		assertTVPairExpired(t, runRoute(h, req))
	})

	t.Run("expired pairing", func(t *testing.T) {
		h := newRouteTestServer(t)
		p := tvPairingFixture()
		p.ExpiresAt = time.Now().Add(-time.Second)
		swapTVPairingByKey(t, &p, nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/tv/pair-state?lang=id", nil)
		req.AddCookie(&http.Cookie{Name: "tv_pending", Value: tvPairKey})
		assertTVPairExpired(t, runRoute(h, req))
	})

	t.Run("claimed then abandoned", func(t *testing.T) {
		h := newRouteTestServer(t)
		p := tvPairingFixture()
		p.ConsumedAt = time.Now()
		p.ExpiresAt = time.Now().Add(-time.Second)
		swapTVPairingByKey(t, &p, nil, nil)
		req := httptest.NewRequest(http.MethodGet, "/tv/pair-state?lang=id", nil)
		req.AddCookie(&http.Cookie{Name: "tv_pending", Value: tvPairKey})
		assertTVPairExpired(t, runRoute(h, req))
	})

	t.Run("no cookie never reaches the repo", func(t *testing.T) {
		h := newRouteTestServer(t)
		prev := tvPairingByKey
		tvPairingByKey = func(_ *Server, _ *http.Request, _ string) (*domain.TVPairing, error) {
			t.Error("a poll without a pending cookie must not query the repo")
			return nil, nil
		}
		t.Cleanup(func() { tvPairingByKey = prev })
		assertTVPairExpired(t, runRoute(h, httptest.NewRequest(http.MethodGet, "/tv/pair-state?lang=id", nil)))
	})
}

// The screen must never wedge. The expired poll is not a dead-end notice: it
// clears the dead pending key and answers the paired branch's own mechanism
// (204 + HX-Redirect), so the wall loads a FRESH pairing — new key, new code,
// new 5-minute TTL — within one 4s tick and nobody has to reach for the TV
// remote. The redirect carries ?layout=queue through the re-pair (rotate is
// the default, so it carries nothing).
func TestTVPairStateExpiredRearms(t *testing.T) {
	for _, tc := range []struct {
		name, query, wantRedirect, wantPoll string
	}{
		{"default shell (rotate)", "", "/tv", `hx-get="/tv/pair-state"`},
		{"legacy split lands on rotate", "?layout=split", "/tv", `hx-get="/tv/pair-state"`},
		{"queue-only shell", "?layout=queue", "/tv?layout=queue", `hx-get="/tv/pair-state?layout=queue"`},
		{"unknown layout stays rotate", "?layout=ROTATE", "/tv", `hx-get="/tv/pair-state"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRouteTestServer(t)
			swapTVClock(t, tvQueueNow)
			swapTVPairingByKey(t, nil, repo.ErrNotFound, nil)
			var created []domain.TVPairing
			swapTVPairingCreate(t, &created, nil)

			req := httptest.NewRequest(http.MethodGet, "/tv/pair-state"+tc.query, nil)
			req.Header.Set("HX-Request", "true")
			req.AddCookie(&http.Cookie{Name: "tv_pending", Value: tvPairKey})
			rec := runRoute(h, req)

			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204 (redirect without a swap)", rec.Code)
			}
			if got := rec.Header().Get("HX-Redirect"); got != tc.wantRedirect {
				t.Fatalf("HX-Redirect = %q, want %q", got, tc.wantRedirect)
			}
			if body := rec.Body.String(); body != "" {
				t.Errorf("re-arming response must not write a body, got %q", body)
			}
			// The clear must be a real deletion with the start's own flags.
			c := tvCookie(rec, "tv_pending")
			if c == nil {
				t.Fatal("expired poll must clear the stale pending cookie")
			}
			if c.Value != "" || c.MaxAge >= 0 || !c.HttpOnly || c.Path != "/" {
				t.Errorf("tv_pending clear = (%q, MaxAge %d, HttpOnly %v, Path %q), want empty/0/true//",
					c.Value, c.MaxAge, c.HttpOnly, c.Path)
			}

			// The browser follows the redirect with the cleared cookie gone: a
			// bare /tv must mint a new pairing row (the loop cannot re-serve the
			// dead key).
			rec2 := runRoute(h, httptest.NewRequest(http.MethodGet, tc.wantRedirect, nil))
			if rec2.Code != http.StatusOK {
				t.Fatalf("reload status = %d, want 200", rec2.Code)
			}
			if len(created) != 1 {
				t.Fatalf("pairings created on reload = %d, want 1", len(created))
			}
			p := created[0]
			if p.PendingKey == tvPairKey {
				t.Error("reload reused the dead pending key")
			}
			if !tvPairCodeRe.MatchString(p.Code) {
				t.Errorf("fresh code = %q, want six digits", p.Code)
			}
			if want := tvQueueNow.Add(5 * time.Minute); !p.ExpiresAt.Equal(want) {
				t.Errorf("fresh expires_at = %s, want %s (5-minute TTL)", p.ExpiresAt, want)
			}
			if out := rec2.Body.String(); !contains(out, p.Code) {
				t.Error("the fresh pairing page does not show its new code")
			}
			if out := rec2.Body.String(); !contains(out, tc.wantPoll) {
				t.Errorf("fresh pairing page missing %q", tc.wantPoll)
			}
		})
	}
}

// A DB blip is not an expired code: answer 500 so htmx keeps the current
// fragment and the 4s poll retries, instead of telling the counter to re-pair.
func TestTVPairStateFailureKeepsFragment(t *testing.T) {
	h := newRouteTestServer(t)
	swapTVPairingByKey(t, nil, errors.New("db down"), nil)

	req := httptest.NewRequest(http.MethodGet, "/tv/pair-state?lang=id", nil)
	req.AddCookie(&http.Cookie{Name: "tv_pending", Value: tvPairKey})
	rec := runRoute(h, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (htmx keeps the fragment)", rec.Code)
	}
	if contains(rec.Body.String(), "kedaluwarsa") {
		t.Error("an outage must not claim the code expired")
	}
}

func assertTVPairExpired(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	out := rec.Body.String()
	for _, want := range []string{"Kode ini sudah kedaluwarsa", `href="/tv"`, "Mulai ulang"} {
		if !contains(out, want) {
			t.Errorf("expired fragment missing %q", want)
		}
	}
	c := tvCookie(rec, "tv_pending")
	if c == nil {
		t.Fatal("expired poll must clear the stale pending cookie")
	}
	if c.Value != "" || c.MaxAge >= 0 {
		t.Errorf("tv_pending = (%q, MaxAge %d), want cleared", c.Value, c.MaxAge)
	}
}
