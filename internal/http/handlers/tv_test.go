package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/http/middleware"
	"spa-booking/internal/repo"

	"github.com/google/uuid"
)

// tvTestToken is a realistic kiosk token (32 bytes hex), distinct from the
// domain test constant so a leaked value is traceable to this file.
const tvTestToken = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"

func tvFixture() domain.TVToken {
	return domain.TVToken{
		ID:         uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		Token:      tvTestToken,
		Label:      "Lobby TV",
		LocationID: uuid.MustParse("22222222-2222-2222-2222-222222222222"),
	}
}

// swapTVActive pins the token lookup seam. The repos are concrete pgx types,
// so the tests substitute the call exactly like queueBookings does.
func swapTVActive(t *testing.T, tok *domain.TVToken, err error, calls *string) {
	t.Helper()
	prev := tvTokenActive
	tvTokenActive = func(_ *Server, _ *http.Request, token string) (*domain.TVToken, error) {
		if calls != nil {
			*calls = token
		}
		return tok, err
	}
	t.Cleanup(func() { tvTokenActive = prev })
}

// adminRequest builds an authenticated POST through the real router so the
// chi URL params, the admin-only middleware, and the CSRF cookie all behave
// as they do in production.
func adminRequest(t *testing.T, method, path string, role domain.Role, form url.Values) *http.Request {
	t.Helper()
	body := ""
	if form != nil {
		form.Set("csrf", "tok")
		body = form.Encode()
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	req.AddCookie(&http.Cookie{Name: "csrf", Value: "tok"})
	return req.WithContext(context.WithValue(req.Context(), middleware.UserKey,
		&domain.User{ID: uuid.New(), Role: role, FullName: "Admin"}))
}

func runRoute(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// A bare /tv (no ?t=) is the pairing entry (ruling #7): the screen mints a
// code and never touches the kiosk-token lookup. The invalid page is reserved
// for a presented-but-bad ?t= token.
func TestTVRouteWithoutTokenStartsPairing(t *testing.T) {
	h := newRouteTestServer(t)
	calls := ""
	swapTVActive(t, nil, repo.ErrNotFound, &calls)
	swapTVPairingCreate(t, nil, nil)

	rec := runRoute(h, httptest.NewRequest(http.MethodGet, "/tv?lang=en", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	out := rec.Body.String()
	for _, want := range []string{"Pair this screen", `hx-get="/tv/pair-state"`} {
		if !contains(out, want) {
			t.Errorf("pairing screen missing %q", want)
		}
	}
	if calls != "" {
		t.Errorf("no token must not reach the lookup seam, got %q", calls)
	}
	if contains(out, "/tv?t=") || contains(out, tvTestToken) {
		t.Error("pairing screen must not echo a kiosk link or token")
	}
}

// A revoked (or unknown) token must be indistinguishable from a missing one:
// same minimal page, no data, and the revocation wins immediately because
// ActiveByToken filters revoked_at in SQL.
func TestTVRouteRevokedTokenShowsInvalidPage(t *testing.T) {
	h := newRouteTestServer(t)
	calls := ""
	swapTVActive(t, nil, repo.ErrNotFound, &calls)

	for _, path := range []string{"/tv?lang=en&t=" + tvTestToken, "/tv/queue?lang=id&t=" + tvTestToken} {
		rec := runRoute(h, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", path, rec.Code)
		}
		out := rec.Body.String()
		if !contains(out, "Display not available") && !contains(out, "Tampilan tidak tersedia") {
			t.Errorf("%s: revoked token did not render the invalid page", path)
		}
		if contains(out, "Baby Massage") || contains(out, tvTestToken) {
			t.Errorf("%s: invalid page leaked queue data", path)
		}
	}
	if calls != tvTestToken {
		t.Errorf("lookup seam got %q, want the presented token", calls)
	}
}

// A live token renders the kiosk shell with a 200 on both the page and the
// poll endpoint.
func TestTVRouteValidTokenShowsShell(t *testing.T) {
	h := newRouteTestServer(t)
	tok := tvFixture()
	swapTVActive(t, &tok, nil, nil)
	swapTVClock(t, tvQueueNow)
	swapTVQueue(t, nil, nil, nil)

	for _, path := range []string{"/tv?lang=en&t=" + tvTestToken, "/tv/queue?lang=en&t=" + tvTestToken} {
		rec := runRoute(h, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", path, rec.Code)
		}
		out := rec.Body.String()
		// The page carries the shell; the poll answers only the band + rows
		// fragment (that is what gets swapped into #tv-live).
		want := "tv-shell"
		if strings.HasPrefix(path, "/tv/queue") {
			want = "tv-band"
		}
		if !contains(out, want) {
			t.Errorf("%s: valid token did not render %q", path, want)
		}
		if contains(out, "Display not available") {
			t.Errorf("%s: valid token rendered the invalid page", path)
		}
	}
}

// tvTokenFrom must re-check the returned row's token in constant time: the
// lookup is by token, but a mis-built query (or a seam gone wrong) must not
// hand a TV someone else's location.
func TestTVTokenFromRejectsMismatchedToken(t *testing.T) {
	s := newFailTestServer(t)
	tok := tvFixture()
	swapTVActive(t, &tok, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/tv?t="+strings.Repeat("0", len(tvTestToken)), nil)
	if got, ok := s.tvTokenFrom(req); ok || got != nil {
		t.Fatalf("mismatched token accepted (got %v)", got)
	}
	req = httptest.NewRequest(http.MethodGet, "/tv", nil)
	if got, ok := s.tvTokenFrom(req); ok || got != nil {
		t.Fatalf("missing token accepted (got %v)", got)
	}
	req = httptest.NewRequest(http.MethodGet, "/tv?t="+tvTestToken, nil)
	if got, ok := s.tvTokenFrom(req); !ok || got == nil {
		t.Fatal("valid token rejected")
	}
	// Revocation wins even when the seam hands back a row whose secret matches
	// (a cached/late row must not reopen a retired screen).
	revoked := tvFixture()
	now := time.Now()
	revoked.RevokedAt = &now
	swapTVActive(t, &revoked, nil, nil)
	if got, ok := s.tvTokenFrom(httptest.NewRequest(http.MethodGet, "/tv?t="+tvTestToken, nil)); ok || got != nil {
		t.Fatal("revoked token accepted")
	}
}

// ---------------------------------------------------------------------------
// The unified credential chain (ruling #7): a live device cookie first, the
// shipped ?t= token second, and the pairing screen as the self-service way
// back in. These tests are the whole point of the pairing plan: the redacted
// view must not care WHICH credential opened it, and a dead credential must
// never leave a screen dark waiting for a human.
// ---------------------------------------------------------------------------

// tvDeviceFixture is a live paired device in the same branch as tvFixture. Its
// stored hash is sha256(tvPairKey), exactly what the portal writes: the
// pairing's pending key IS the device secret (ruling #9), so the cookie the
// pair-state poll minted verifies without any secret travelling to the counter.
func tvDeviceFixture() domain.TVDevice {
	return domain.TVDevice{
		ID:         uuid.MustParse("55555555-5555-5555-5555-555555555555"),
		SecretHash: domain.TVSecretHash(tvPairKey),
		Label:      "Lobby TV",
		LocationID: uuid.MustParse("22222222-2222-2222-2222-222222222222"),
	}
}

// tvDeviceCookieValue is the exact cookie the pair-state poll sets
// (domain.TVDeviceCookie), so these tests present the real wire shape.
func tvDeviceCookieValue(id uuid.UUID, secret string) string {
	return domain.TVDeviceCookie(id, secret)
}

// swapTVDevice pins the device lookup seam. The repos are concrete pgx types,
// so the tests substitute the call exactly like tvTokenActive does.
func swapTVDevice(t *testing.T, dev *domain.TVDevice, err error, got *uuid.UUID) {
	t.Helper()
	prev := tvDeviceByID
	tvDeviceByID = func(_ *Server, _ *http.Request, id uuid.UUID) (*domain.TVDevice, error) {
		if got != nil {
			*got = id
		}
		return dev, err
	}
	t.Cleanup(func() { tvDeviceByID = prev })
}

// tvTouchCall is one last-seen stamp as it reached the repo seam.
type tvTouchCall struct {
	id        uuid.UUID
	threshold time.Duration
}

// swapTVDeviceTouch pins the last-seen seam and records every stamp, so the
// test asserts the device id and the 1-minute throttle argument rather than
// trusting the call site.
func swapTVDeviceTouch(t *testing.T, err error, calls *[]tvTouchCall) {
	t.Helper()
	prev := tvDeviceTouch
	tvDeviceTouch = func(_ *Server, _ *http.Request, id uuid.UUID, threshold time.Duration) error {
		if calls != nil {
			*calls = append(*calls, tvTouchCall{id: id, threshold: threshold})
		}
		return err
	}
	t.Cleanup(func() { tvDeviceTouch = prev })
}

// TestTVCredentialChain is the ruling-#7 table: every credential state maps to
// exactly one face of /tv, and the two failure faces never blur into each
// other (a dead cookie self-heals into pairing, a bad token stays dark).
func TestTVCredentialChain(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name   string
		cookie string // tv_device value, empty = no cookie
		query  string
		dev    *domain.TVDevice
		devErr error
		token  *domain.TVToken
		want   []string
		absent []string
	}{
		{
			name:   "live device cookie renders the view",
			cookie: tvDeviceCookieValue(tvDeviceFixture().ID, tvPairKey),
			dev:    func() *domain.TVDevice { d := tvDeviceFixture(); return &d }(),
			want:   []string{"tv-shell", "Sari W."},
			absent: []string{"Hubungkan layar ini", "Tampilan tidak tersedia"},
		},
		{
			name:  "live token renders the view",
			query: "t=" + tvTestToken,
			token: func() *domain.TVToken { tok := tvFixture(); return &tok }(),
			want:  []string{"tv-shell", "Sari W."},
			absent: []string{
				"Hubungkan layar ini", "Tampilan tidak tersedia",
			},
		},
		{
			name:   "revoked device cookie re-opens pairing",
			cookie: tvDeviceCookieValue(tvDeviceFixture().ID, tvPairKey),
			dev: func() *domain.TVDevice {
				d := tvDeviceFixture()
				d.RevokedAt = &now
				return &d
			}(),
			want:   []string{"Hubungkan layar ini"},
			absent: []string{"Tampilan tidak tersedia", "Sari W."},
		},
		{
			name:   "unknown device cookie re-opens pairing",
			cookie: tvDeviceCookieValue(tvDeviceFixture().ID, tvPairKey),
			devErr: repo.ErrNotFound,
			want:   []string{"Hubungkan layar ini"},
			absent: []string{"Tampilan tidak tersedia", "Sari W."},
		},
		{
			name:   "no credential starts pairing",
			want:   []string{"Hubungkan layar ini"},
			absent: []string{"Tampilan tidak tersedia", "Sari W."},
		},
		{
			name:  "bad token keeps the invalid page",
			query: "t=" + strings.Repeat("0", len(tvTestToken)),
			want:  []string{"Tampilan tidak tersedia"},
			absent: []string{
				"Hubungkan layar ini", "Sari W.",
			},
		},
		{
			// The two credentials are independent: retiring a token URL must not
			// black out a screen that still holds a live device.
			name:   "dead device cookie plus a live token renders the view",
			cookie: tvDeviceCookieValue(tvDeviceFixture().ID, tvPairKey),
			query:  "t=" + tvTestToken,
			devErr: repo.ErrNotFound,
			token:  func() *domain.TVToken { tok := tvFixture(); return &tok }(),
			want:   []string{"tv-shell", "Sari W."},
			absent: []string{"Hubungkan layar ini", "Tampilan tidak tersedia"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newRouteTestServer(t)
			switch {
			case tc.token != nil:
				swapTVActive(t, tc.token, nil, nil)
			case strings.Contains(tc.query, "t="):
				// Present but unknown: the token path must answer the invalid page.
				swapTVActive(t, nil, repo.ErrNotFound, nil)
			default:
				prev := tvTokenActive
				tvTokenActive = func(_ *Server, _ *http.Request, _ string) (*domain.TVToken, error) {
					t.Error("no ?t= must not reach the token seam")
					return nil, nil
				}
				t.Cleanup(func() { tvTokenActive = prev })
			}
			swapTVDevice(t, tc.dev, tc.devErr, nil)
			if tc.cookie == "" {
				prev := tvDeviceByID
				tvDeviceByID = func(_ *Server, _ *http.Request, _ uuid.UUID) (*domain.TVDevice, error) {
					t.Error("no tv_device cookie must not reach the device seam")
					return nil, nil
				}
				t.Cleanup(func() { tvDeviceByID = prev })
			}
			swapTVDeviceTouch(t, nil, nil)
			swapTVPairingCreate(t, nil, nil)
			swapTVClock(t, tvQueueNow)
			swapTVQueue(t, []domain.Booking{tvHostileBooking()}, nil, nil)

			path := "/tv?lang=id"
			if tc.query != "" {
				path += "&" + tc.query
			}
			req := httptest.NewRequest(http.MethodGet, path, nil)
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: "tv_device", Value: tc.cookie})
			}
			rec := runRoute(h, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			out := rec.Body.String()
			for _, want := range tc.want {
				if !contains(out, want) {
					t.Errorf("missing %q", want)
				}
			}
			for _, absent := range tc.absent {
				if contains(out, absent) {
					t.Errorf("unexpected %q", absent)
				}
			}
		})
	}
}

// A live device cookie renders the same redacted view as a token, polls without
// a token in the URL (the cookie rides along), and stamps last-seen with the
// repo's 1-minute throttle.
func TestTVDeviceCookieViewStampsLastSeen(t *testing.T) {
	h := newRouteTestServer(t)
	dev := tvDeviceFixture()
	var lookedUp uuid.UUID
	swapTVDevice(t, &dev, nil, &lookedUp)
	var touches []tvTouchCall
	swapTVDeviceTouch(t, nil, &touches)
	swapTVClock(t, tvQueueNow)
	swapTVQueue(t, []domain.Booking{tvHostileBooking()}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/tv?lang=id", nil)
	req.AddCookie(&http.Cookie{Name: "tv_device", Value: tvDeviceCookieValue(dev.ID, tvPairKey)})
	rec := runRoute(h, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	out := rec.Body.String()
	if !contains(out, "tv-shell") || !contains(out, "Sari W.") {
		t.Fatal("the paired screen did not render the redacted view")
	}
	if contains(out, `hx-get="/tv/queue?`) {
		t.Error("a paired screen must not put a token in its poll URL")
	}
	if lookedUp != dev.ID {
		t.Errorf("looked up %s, want the cookie's device id %s", lookedUp, dev.ID)
	}
	if len(touches) != 1 {
		t.Fatalf("last-seen stamps = %d, want 1", len(touches))
	}
	if touches[0].id != dev.ID || touches[0].threshold != time.Minute {
		t.Errorf("stamp = (%s, %v), want (%s, 1m)", touches[0].id, touches[0].threshold, dev.ID)
	}
}

// The 30s poll authenticates by the same cookie, so a paired screen keeps
// living without any URL secret — and it refreshes last-seen (the shipped
// UPDATE throttles itself to one write a minute, which is exactly the poll's
// cadence).
func TestTVQueuePartialDeviceCookieShowsRows(t *testing.T) {
	h := newRouteTestServer(t)
	dev := tvDeviceFixture()
	swapTVDevice(t, &dev, nil, nil)
	var touches []tvTouchCall
	swapTVDeviceTouch(t, nil, &touches)
	swapTVClock(t, tvQueueNow)
	swapTVQueue(t, []domain.Booking{tvHostileBooking()}, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/tv/queue?lang=id", nil)
	req.Header.Set("HX-Request", "true")
	req.AddCookie(&http.Cookie{Name: "tv_device", Value: tvDeviceCookieValue(dev.ID, tvPairKey)})
	rec := runRoute(h, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	out := rec.Body.String()
	if !contains(out, "tv-band") || !contains(out, "Sari W.") {
		t.Error("the paired poll did not render the queue fragment")
	}
	if len(touches) != 1 || touches[0].id != dev.ID {
		t.Errorf("poll last-seen stamps = %v, want one for %s", touches, dev.ID)
	}
}

// Ruling #7's self-healing half: a PRESENT-but-dead cookie is cleared and the
// screen falls back to the pairing handshake — never the invalid page, which
// would leave the wall dark until a human visits the counter.
func TestTVRouteDeadDeviceCookieRepairs(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name   string
		cookie string
		dev    *domain.TVDevice
		err    error
		// noLookup pins that garbage never reaches the database (the handshake's
		// parse gate), so the seam is a tripwire instead of a stub.
		noLookup bool
	}{
		{
			name:   "revoked device",
			cookie: tvDeviceCookieValue(tvDeviceFixture().ID, tvPairKey),
			dev: func() *domain.TVDevice {
				d := tvDeviceFixture()
				d.RevokedAt = &now
				return &d
			}(),
		},
		{
			name:   "swept device",
			cookie: tvDeviceCookieValue(tvDeviceFixture().ID, tvPairKey),
			err:    repo.ErrNotFound,
		},
		{
			name:   "stolen id with the wrong secret",
			cookie: tvDeviceCookieValue(tvDeviceFixture().ID, strings.Repeat("f", len(tvPairKey))),
			dev:    func() *domain.TVDevice { d := tvDeviceFixture(); return &d }(),
		},
		{
			name:     "malformed cookie",
			cookie:   "not-a-device",
			noLookup: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newRouteTestServer(t)
			var created []domain.TVPairing
			swapTVPairingCreate(t, &created, nil)
			swapTVDevice(t, tc.dev, tc.err, nil)
			if tc.noLookup {
				prev := tvDeviceByID
				tvDeviceByID = func(_ *Server, _ *http.Request, _ uuid.UUID) (*domain.TVDevice, error) {
					t.Error("a malformed cookie must not reach the repo")
					return nil, nil
				}
				t.Cleanup(func() { tvDeviceByID = prev })
			}
			swapTVDeviceTouch(t, nil, nil)
			swapTVClock(t, tvQueueNow)
			swapTVQueue(t, nil, nil, nil)

			req := httptest.NewRequest(http.MethodGet, "/tv?lang=id", nil)
			req.AddCookie(&http.Cookie{Name: "tv_device", Value: tc.cookie})
			rec := runRoute(h, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			out := rec.Body.String()
			if !contains(out, "Hubungkan layar ini") {
				t.Error("a dead device cookie must re-open the pairing screen")
			}
			if contains(out, "Tampilan tidak tersedia") {
				t.Error("a dead device cookie must not render the invalid page")
			}
			if len(created) != 1 {
				t.Errorf("pairing rows created = %d, want 1 (the screen re-enters the handshake)", len(created))
			}
			c := tvCookie(rec, "tv_device")
			if c == nil {
				t.Fatal("the dead cookie was not cleared")
			}
			if c.Value != "" || c.MaxAge >= 0 {
				t.Errorf("tv_device = (%q, MaxAge %d), want cleared", c.Value, c.MaxAge)
			}
			if !c.HttpOnly || c.Path != "/" || c.SameSite != http.SameSiteLaxMode {
				t.Errorf("cleared tv_device flags = HttpOnly:%v Path:%q SameSite:%v, want true///Lax",
					c.HttpOnly, c.Path, c.SameSite)
			}
		})
	}
}

// A lookup outage is NOT a dead credential: clearing the cookie there would
// cost the wall a counter visit for a transient blip, so the chain answers the
// invalid page and leaves the credential alone.
func TestTVRouteDeviceLookupOutageKeepsCookie(t *testing.T) {
	h := newRouteTestServer(t)
	swapTVDevice(t, nil, errors.New("db down"), nil)
	swapTVDeviceTouch(t, nil, nil)
	prevCreate := tvPairingCreate
	tvPairingCreate = func(_ *Server, _ *http.Request, _ *domain.TVPairing) error {
		t.Error("an outage must not start a pairing")
		return nil
	}
	t.Cleanup(func() { tvPairingCreate = prevCreate })

	req := httptest.NewRequest(http.MethodGet, "/tv?lang=id", nil)
	req.AddCookie(&http.Cookie{Name: "tv_device", Value: tvDeviceCookieValue(tvDeviceFixture().ID, tvPairKey)})
	rec := runRoute(h, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	out := rec.Body.String()
	if !contains(out, "Tampilan tidak tersedia") {
		t.Error("an outage must render the invalid page, not a pairing")
	}
	if contains(out, "Hubungkan layar ini") {
		t.Error("an outage must not re-open pairing")
	}
	if c := tvCookie(rec, "tv_device"); c != nil {
		t.Error("an outage must not clear the device cookie")
	}
}

// A failed last-seen stamp is diagnostics, not a data path: the wall keeps
// rendering (a log line is the whole cost).
func TestTVDeviceTouchFailureStillRenders(t *testing.T) {
	h := newRouteTestServer(t)
	dev := tvDeviceFixture()
	swapTVDevice(t, &dev, nil, nil)
	swapTVDeviceTouch(t, errors.New("db down"), nil)
	swapTVClock(t, tvQueueNow)
	swapTVQueue(t, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/tv?lang=en", nil)
	req.AddCookie(&http.Cookie{Name: "tv_device", Value: tvDeviceCookieValue(dev.ID, tvPairKey)})
	rec := runRoute(h, req)

	if rec.Code != http.StatusOK || !contains(rec.Body.String(), "tv-shell") {
		t.Fatalf("status = %d, want the kiosk shell despite a failed stamp", rec.Code)
	}
}

// The poll never swaps a pairing screen into the queue slot: when its
// credential is gone it asks htmx for a full reload, so /tv's chain decides
// (and clears the dead cookie on the way). A naked non-htmx poll keeps the
// shipped invalid page.
func TestTVQueuePartialWithoutCredentialAsksForReload(t *testing.T) {
	cases := []struct{ name, cookie string }{
		{"dead device cookie", tvDeviceCookieValue(tvDeviceFixture().ID, tvPairKey)},
		{"no credential at all", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newRouteTestServer(t)
			swapTVDevice(t, nil, repo.ErrNotFound, nil)
			swapTVDeviceTouch(t, nil, nil)

			req := httptest.NewRequest(http.MethodGet, "/tv/queue?lang=id", nil)
			req.Header.Set("HX-Request", "true")
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: "tv_device", Value: tc.cookie})
			}
			rec := runRoute(h, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got := rec.Header().Get("HX-Refresh"); got != "true" {
				t.Errorf("HX-Refresh = %q, want true", got)
			}
			if body := rec.Body.String(); body != "" {
				t.Errorf("a reload answer must be empty, got %q", body)
			}
			if c := tvCookie(rec, "tv_device"); tc.cookie != "" && (c == nil || c.MaxAge >= 0) {
				t.Error("a dead device cookie must be cleared on the poll too")
			}
		})
	}

	t.Run("plain browser keeps the invalid page", func(t *testing.T) {
		h := newRouteTestServer(t)
		swapTVDevice(t, nil, repo.ErrNotFound, nil)
		swapTVDeviceTouch(t, nil, nil)
		rec := runRoute(h, httptest.NewRequest(http.MethodGet, "/tv/queue?lang=id", nil))
		if rec.Code != http.StatusOK || !contains(rec.Body.String(), "Tampilan tidak tersedia") {
			t.Errorf("status = %d, want the invalid page for a non-htmx poll", rec.Code)
		}
		if got := rec.Header().Get("HX-Refresh"); got != "" {
			t.Errorf("a plain browser must not be told to refresh, got %q", got)
		}
	})
}

// The admin page must show the full link (for typing on the TV) plus the
// rotating-layout variant, and a revoke control per active token.
func TestAdminTVPageShowsURLs(t *testing.T) {
	h := newRouteTestServer(t)
	tok := tvFixture()
	swapTVTokenList(t, []domain.TVToken{tok}, nil)
	swapTVLocations(t, []domain.Location{{ID: tok.LocationID, Name: "Kayu Putih", Active: true}}, nil)
	swapTVDeviceList(t, nil, nil)

	rec := runRoute(h, adminRequest(t, http.MethodGet, "/admin/tv?lang=en", domain.RoleAdmin, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	out := rec.Body.String()
	for _, want := range []string{
		"Lobby TV",
		"/tv?t=" + tvTestToken,
		"layout=queue",
		"/admin/tv/revoke/" + tok.ID.String(),
		`action="/admin/tv/generate"`,
		"Kayu Putih",
		"Active links",
	} {
		if !contains(out, want) {
			t.Errorf("admin TV page missing %q", want)
		}
	}
	// The default URL is the rotate deck: the queue-only variant is the only
	// one that carries a layout param.
	if contains(out, "layout=rotate") {
		t.Error("admin TV page still advertises the retired rotate parameter")
	}
}

// Generating a link mints the same shape guest tokens use (32 random bytes,
// hex) and records the branch the TV belongs to.
func TestAdminTVGenerateCreatesToken(t *testing.T) {
	h := newRouteTestServer(t)
	saved := domain.TVToken{}
	prev := tvTokenCreate
	tvTokenCreate = func(_ *Server, _ *http.Request, tok *domain.TVToken) error {
		saved = *tok
		return nil
	}
	t.Cleanup(func() { tvTokenCreate = prev })
	locID := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	rec := runRoute(h, adminRequest(t, http.MethodPost, "/admin/tv/generate", domain.RoleAdmin, url.Values{
		"label":       {"Lobby TV"},
		"location_id": {locID.String()},
	}))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if !strings.HasPrefix(rec.Header().Get("Location"), "/admin/tv?success=") {
		t.Errorf("Location = %q, want a success redirect", rec.Header().Get("Location"))
	}
	if len(saved.Token) != 64 {
		t.Errorf("token length = %d, want 64 hex chars (32 random bytes)", len(saved.Token))
	}
	for _, r := range saved.Token {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("token %q is not lowercase hex", saved.Token)
		}
	}
	if saved.Label != "Lobby TV" {
		t.Errorf("label = %q, want Lobby TV", saved.Label)
	}
	if saved.LocationID != locID {
		t.Errorf("location = %s, want %s", saved.LocationID, locID)
	}
}

// A generate POST without a usable branch is a form error, not a 500: the
// HTMX path must toast and keep the page.
func TestAdminTVGenerateRejectsBadLocation(t *testing.T) {
	h := newRouteTestServer(t)
	prev := tvTokenCreate
	tvTokenCreate = func(_ *Server, _ *http.Request, _ *domain.TVToken) error {
		t.Error("invalid location must not reach the repo")
		return nil
	}
	t.Cleanup(func() { tvTokenCreate = prev })

	req := adminRequest(t, http.MethodPost, "/admin/tv/generate", domain.RoleAdmin, url.Values{
		"label":       {"Lobby TV"},
		"location_id": {"not-a-uuid"},
	})
	req.Header.Set("HX-Request", "true")
	rec := runRoute(h, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if body := rec.Body.String(); body != "" {
		t.Errorf("HTMX failure must not write a body, got %q", body)
	}
	if kind, _ := decodeToast(t, rec); kind != "error" {
		t.Errorf("toast kind = %q, want error", kind)
	}
}

// A repo failure on generate takes the staffFail contract (toast + no swap),
// never a bare 500 that HTMX would silently swallow.
func TestAdminTVGenerateFailureToasts(t *testing.T) {
	h := newRouteTestServer(t)
	prev := tvTokenCreate
	tvTokenCreate = func(_ *Server, _ *http.Request, _ *domain.TVToken) error {
		return fmt.Errorf("db down")
	}
	t.Cleanup(func() { tvTokenCreate = prev })

	req := adminRequest(t, http.MethodPost, "/admin/tv/generate", domain.RoleAdmin, url.Values{
		"label":       {"Lobby TV"},
		"location_id": {uuid.New().String()},
	})
	req.Header.Set("HX-Request", "true")
	rec := runRoute(h, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if body := rec.Body.String(); body != "" {
		t.Errorf("HTMX failure must not write a body, got %q", body)
	}
	if got := rec.Header().Get("HX-Reswap"); got != "none" {
		t.Errorf("HX-Reswap = %q, want none", got)
	}
	if kind, msg := decodeToast(t, rec); kind != "error" || msg == "" {
		t.Errorf("toast = (%q, %q), want an error with copy", kind, msg)
	}
}

// Revoking passes the id from the URL through to the repo and redirects back.
func TestAdminTVRevokeCallsSeam(t *testing.T) {
	h := newRouteTestServer(t)
	tok := tvFixture()
	var revoked uuid.UUID
	prev := tvTokenRevoke
	tvTokenRevoke = func(_ *Server, _ *http.Request, id uuid.UUID) error {
		revoked = id
		return nil
	}
	t.Cleanup(func() { tvTokenRevoke = prev })

	rec := runRoute(h, adminRequest(t, http.MethodPost, "/admin/tv/revoke/"+tok.ID.String(), domain.RoleAdmin, url.Values{}))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if revoked != tok.ID {
		t.Errorf("revoked id = %s, want %s", revoked, tok.ID)
	}
}

func TestAdminTVRevokeFailureToasts(t *testing.T) {
	h := newRouteTestServer(t)
	prev := tvTokenRevoke
	tvTokenRevoke = func(_ *Server, _ *http.Request, _ uuid.UUID) error {
		return fmt.Errorf("db down")
	}
	t.Cleanup(func() { tvTokenRevoke = prev })

	req := adminRequest(t, http.MethodPost, "/admin/tv/revoke/"+uuid.New().String(), domain.RoleAdmin, url.Values{})
	req.Header.Set("HX-Request", "true")
	rec := runRoute(h, req)

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

// Ruling #10: token management is admin-only. CS reaching the URL gets a 403
// from the group middleware, and an anonymous request goes to login.
func TestAdminTVIsAdminOnly(t *testing.T) {
	h := newRouteTestServer(t)
	if rec := runRoute(h, adminRequest(t, http.MethodGet, "/admin/tv", domain.RoleCS, nil)); rec.Code != http.StatusForbidden {
		t.Errorf("cs status = %d, want 403", rec.Code)
	}
	anon := httptest.NewRequest(http.MethodGet, "/admin/tv", nil)
	if rec := runRoute(h, anon); rec.Code != http.StatusSeeOther {
		t.Errorf("anonymous status = %d, want 303 to login", rec.Code)
	}
}

// The admin hub must link the new page; a page nobody can find may as well
// not exist.
func TestAdminHomeLinksToTV(t *testing.T) {
	s := newFailTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserKey,
		&domain.User{ID: uuid.New(), Role: domain.RoleAdmin, FullName: "Admin"}))
	s.render(rec, req, "admin_home.html", pageData{})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !contains(rec.Body.String(), `href="/admin/tv"`) {
		t.Error("admin home is missing the /admin/tv link")
	}
}

// swapTVTokenList / swapTVLocations are the admin page's two data seams.
func swapTVTokenList(t *testing.T, list []domain.TVToken, err error) {
	t.Helper()
	prev := tvTokenList
	tvTokenList = func(_ *Server, _ *http.Request) ([]domain.TVToken, error) { return list, err }
	t.Cleanup(func() { tvTokenList = prev })
}

func swapTVLocations(t *testing.T, locs []domain.Location, err error) {
	t.Helper()
	prev := tvActiveLocations
	tvActiveLocations = func(_ *Server, _ *http.Request) ([]domain.Location, error) { return locs, err }
	t.Cleanup(func() { tvActiveLocations = prev })
}
