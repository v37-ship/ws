package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"spa-booking/internal/i18n"
	"spa-booking/internal/repo"

	"github.com/google/uuid"
)

// The portal pairing page is the counter's half of the handshake: the CS types
// the code the TV shows, picks the branch and (optionally) names the device.
// Everything here is device metadata only — labels, branches, timestamps —
// never booking data.

var tvPortalLocID = uuid.MustParse("22222222-2222-2222-2222-222222222222")

func tvPairingFixturePtr() *domain.TVPairing {
	p := tvPairingFixture()
	return &p
}

func tvPortalLocation() domain.Location {
	return domain.Location{ID: tvPortalLocID, Name: "Kayu Putih", Active: true}
}

func tvPortalDevice() domain.TVDevice {
	at := tvSweepTime
	return domain.TVDevice{
		ID:         uuid.MustParse("44444444-4444-4444-4444-444444444444"),
		SecretHash: "cafebabecafebabecafebabecafebabecafebabecafebabecafebabecafebabe",
		Label:      "Lobby TV",
		LocationID: tvPortalLocID,
		PairedAt:   &at,
	}
}

// tvPortalUser builds the authenticated identity the LoadUser middleware would
// have resolved. Unlike adminRequest it hands the user back, because the pair
// flow records PairedBy and the rate limiter keys on the user id.
func tvPortalUserRequest(t *testing.T, method, path string, u *domain.User, form url.Values) *http.Request {
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
	return req.WithContext(context.WithValue(req.Context(), middleware.UserKey, u))
}

// The seam swaps mirror the tvTokenList/tvTokenRevoke precedent: the repos are
// concrete pgx types, so the tests substitute the calls and capture what the
// handler asked for.
func swapTVDeviceList(t *testing.T, list []domain.TVDevice, err error) {
	t.Helper()
	prev := tvDeviceList
	tvDeviceList = func(_ *Server, _ *http.Request) ([]domain.TVDevice, error) { return list, err }
	t.Cleanup(func() { tvDeviceList = prev })
}

func swapTVDeviceCreate(t *testing.T, err error) **domain.TVDevice {
	t.Helper()
	var saved *domain.TVDevice
	prev := tvDeviceCreate
	tvDeviceCreate = func(_ *Server, _ *http.Request, d *domain.TVDevice) error {
		if d.ID == uuid.Nil {
			d.ID = uuid.New()
		}
		saved = d
		return err
	}
	t.Cleanup(func() { tvDeviceCreate = prev })
	return &saved
}

type tvPairAttempt struct {
	Code string
	Loc  uuid.UUID
}

func swapTVPairingConsume(t *testing.T, p *domain.TVPairing, err error) *[]tvPairAttempt {
	t.Helper()
	var calls []tvPairAttempt
	prev := tvPairingConsume
	tvPairingConsume = func(_ *Server, _ *http.Request, code string, loc uuid.UUID) (*domain.TVPairing, error) {
		calls = append(calls, tvPairAttempt{Code: code, Loc: loc})
		return p, err
	}
	t.Cleanup(func() { tvPairingConsume = prev })
	return &calls
}

func swapTVPairingLink(t *testing.T, err error) *[][2]uuid.UUID {
	t.Helper()
	var calls [][2]uuid.UUID
	prev := tvPairingLink
	tvPairingLink = func(_ *Server, _ *http.Request, pairingID, deviceID uuid.UUID) error {
		calls = append(calls, [2]uuid.UUID{pairingID, deviceID})
		return err
	}
	t.Cleanup(func() { tvPairingLink = prev })
	return &calls
}

func swapTVDeviceRevoke(t *testing.T, err error) *[]uuid.UUID {
	t.Helper()
	var calls []uuid.UUID
	prev := tvDeviceRevoke
	tvDeviceRevoke = func(_ *Server, _ *http.Request, id uuid.UUID) error {
		calls = append(calls, id)
		return err
	}
	t.Cleanup(func() { tvDeviceRevoke = prev })
	return &calls
}

// The CS-facing page: the pair form (code, branch, optional label) plus a
// read-only device list. The branch select auto-picks when the studio has
// exactly one active location (ruling #5 keeps the select visible for
// everyone; `domain.User` carries no branch binding).
func TestPortalTVPageRendersFormAndDevicesForCS(t *testing.T) {
	h := newRouteTestServer(t)
	swapTVLocations(t, []domain.Location{tvPortalLocation()}, nil)
	swapTVDeviceList(t, []domain.TVDevice{tvPortalDevice()}, nil)

	rec := runRoute(h, tvPortalUserRequest(t, http.MethodGet, "/portal/tv?lang=en",
		&domain.User{ID: uuid.New(), Role: domain.RoleCS, FullName: "CS"}, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	out := rec.Body.String()
	for _, want := range []string{
		"Pair a TV",
		`action="/portal/tv/pair"`,
		`hx-post="/portal/tv/pair"`,
		`name="code"`,
		`name="location_id"`,
		`name="label"`,
		`<option value="` + tvPortalLocID.String() + `" selected>`,
		"Paired TVs",
		"Lobby TV",
		"Kayu Putih",
		"21 Nov 2026 17:00", // paired-at, rendered in WIB
		"Not yet",           // never seen
		`href="/portal/tv"`, // the portal nav link
	} {
		if !contains(out, want) {
			t.Errorf("portal TV page missing %q", want)
		}
	}
	if contains(out, "/portal/tv/device/") {
		t.Error("CS must see the device list read-only, no revoke form")
	}
	if contains(out, tvPortalDevice().SecretHash) {
		t.Error("the device secret hash must never reach the page")
	}
}

// Ruling: CS pairs, admin revokes. The revoke control renders only for the
// admin role, on the same page CS reads.
func TestPortalTVPageShowsRevokeToAdminOnly(t *testing.T) {
	h := newRouteTestServer(t)
	swapTVLocations(t, []domain.Location{tvPortalLocation()}, nil)
	swapTVDeviceList(t, []domain.TVDevice{tvPortalDevice()}, nil)

	rec := runRoute(h, tvPortalUserRequest(t, http.MethodGet, "/portal/tv?lang=en",
		&domain.User{ID: uuid.New(), Role: domain.RoleAdmin, FullName: "Admin"}, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	out := rec.Body.String()
	if !contains(out, `/portal/tv/device/`+tvPortalDevice().ID.String()+`/revoke`) {
		t.Error("admin page is missing the revoke action")
	}
	if !contains(out, "Revoke this TV?") {
		t.Error("admin page is missing the revoke confirmation copy")
	}
}

// The happy path is the cross-task contract: the device secret is DERIVED from
// the pairing's pending key (the TV already holds that plaintext), never a
// fresh secret — otherwise the screen could never present its credential.
func TestPortalTVPairMintsDeviceFromPendingKey(t *testing.T) {
	h := newRouteTestServer(t)
	swapTVLocations(t, []domain.Location{tvPortalLocation()}, nil)
	pairing := tvPairingFixture()
	consume := swapTVPairingConsume(t, &pairing, nil)
	saved := swapTVDeviceCreate(t, nil)
	links := swapTVPairingLink(t, nil)
	user := &domain.User{ID: uuid.New(), Role: domain.RoleCS, FullName: "CS"}

	req := tvPortalUserRequest(t, http.MethodPost, "/portal/tv/pair?lang=en", user, url.Values{
		"code":        {pairing.Code},
		"location_id": {tvPortalLocID.String()},
		"label":       {"Lobby TV"},
	})
	req.Header.Set("HX-Request", "true")
	rec := runRoute(h, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (htmx redirect)", rec.Code)
	}
	loc := rec.Header().Get("HX-Redirect")
	if !strings.HasPrefix(loc, "/portal/tv?success=") {
		t.Fatalf("HX-Redirect = %q, want /portal/tv?success=…", loc)
	}
	if msg, err := url.QueryUnescape(strings.TrimPrefix(loc, "/portal/tv?success=")); err != nil || msg != "Lobby TV paired" {
		t.Errorf("success copy = %q (%v), want %q", msg, err, "Lobby TV paired")
	}

	// The consume call carries exactly what the CS typed.
	if len(*consume) != 1 || (*consume)[0].Code != pairing.Code || (*consume)[0].Loc != tvPortalLocID {
		t.Errorf("consume calls = %+v, want one for code %q at %s", *consume, pairing.Code, tvPortalLocID)
	}

	dev := *saved
	if dev == nil {
		t.Fatal("no device was minted")
	}
	sum := sha256.Sum256([]byte(pairing.PendingKey))
	wantHash := hex.EncodeToString(sum[:])
	if dev.SecretHash != wantHash {
		t.Errorf("SecretHash = %q, want sha256(pending key) = %q", dev.SecretHash, wantHash)
	}
	if !dev.SecretOK(pairing.PendingKey) {
		t.Error("the minted device cannot verify the TV's pending key: the screen would be dead")
	}
	if dev.PairedBy == nil || *dev.PairedBy != user.ID {
		t.Errorf("PairedBy = %v, want the pairing staff user %s", dev.PairedBy, user.ID)
	}
	if dev.Label != "Lobby TV" || dev.LocationID != tvPortalLocID {
		t.Errorf("device = (%q, %s), want (Lobby TV, %s)", dev.Label, dev.LocationID, tvPortalLocID)
	}

	// Linking closes the handshake the TV polls for (ruling #6).
	if len(*links) != 1 {
		t.Fatalf("link calls = %d, want 1", len(*links))
	}
	if (*links)[0][0] != pairing.ID || (*links)[0][1] != dev.ID {
		t.Errorf("link = %v, want pairing %s → device %s", (*links)[0], pairing.ID, dev.ID)
	}
}

// Wrong, expired and consumed codes all collapse into one honest message: the
// repo's atomic Consume answers ErrNotFound for every miss, so the portal
// cannot be used as a code oracle. No device may be minted on any of them.
func TestPortalTVPairBadCodeIsOneHonestMessage(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"wrong", repo.ErrNotFound},
		{"expired", repo.ErrNotFound},
		{"consumed", repo.ErrNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRouteTestServer(t)
			swapTVLocations(t, []domain.Location{tvPortalLocation()}, nil)
			swapTVPairingConsume(t, nil, tc.err)
			saved := swapTVDeviceCreate(t, nil)
			links := swapTVPairingLink(t, nil)

			req := tvPortalUserRequest(t, http.MethodPost, "/portal/tv/pair?lang=en",
				&domain.User{ID: uuid.New(), Role: domain.RoleCS}, url.Values{
					"code":        {"482913"},
					"location_id": {tvPortalLocID.String()},
				})
			req.Header.Set("HX-Request", "true")
			rec := runRoute(h, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if body := rec.Body.String(); body != "" {
				t.Errorf("HTMX failure must not write a body, got %q", body)
			}
			kind, msg := decodeToast(t, rec)
			if kind != "error" || msg != i18n.T("en", "tv.pr.bad") {
				t.Errorf("toast = (%q, %q), want error %q", kind, msg, i18n.T("en", "tv.pr.bad"))
			}
			if *saved != nil {
				t.Error("a code miss must never mint a device")
			}
			if len(*links) != 0 {
				t.Errorf("a code miss must never link a device, got %d links", len(*links))
			}
		})
	}
}

// A code that is not exactly six digits cannot be live, so it takes the same
// single message as a wrong one — the shape rule leaks nothing.
func TestPortalTVPairRejectsMalformedCode(t *testing.T) {
	h := newRouteTestServer(t)
	swapTVLocations(t, []domain.Location{tvPortalLocation()}, nil)
	consume := swapTVPairingConsume(t, nil, repo.ErrNotFound)
	saved := swapTVDeviceCreate(t, nil)

	for _, code := range []string{"", "   ", "12345", "1234567", "12a456"} {
		req := tvPortalUserRequest(t, http.MethodPost, "/portal/tv/pair?lang=en",
			&domain.User{ID: uuid.New(), Role: domain.RoleCS}, url.Values{
				"code":        {code},
				"location_id": {tvPortalLocID.String()},
			})
		req.Header.Set("HX-Request", "true")
		rec := runRoute(h, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("code %q: status = %d, want 400", code, rec.Code)
		}
		if _, msg := decodeToast(t, rec); msg != i18n.T("en", "tv.pr.bad") {
			t.Errorf("code %q: toast = %q, want %q", code, msg, i18n.T("en", "tv.pr.bad"))
		}
	}
	if len(*consume) != 0 {
		t.Errorf("malformed codes must not reach the repo, got %d calls", len(*consume))
	}
	if *saved != nil {
		t.Error("no device may be minted from a malformed code")
	}
}

// Ruling #5: every staff member picks the branch (there is no per-user branch
// binding), and a missing or stale value is a form error — never a 500 from the
// foreign key.
func TestPortalTVPairRequiresActiveLocation(t *testing.T) {
	for _, loc := range []string{"", "not-a-uuid", uuid.New().String()} {
		t.Run("loc="+loc, func(t *testing.T) {
			h := newRouteTestServer(t)
			swapTVLocations(t, []domain.Location{tvPortalLocation()}, nil)
			consume := swapTVPairingConsume(t, tvPairingFixturePtr(), nil)

			req := tvPortalUserRequest(t, http.MethodPost, "/portal/tv/pair?lang=en",
				&domain.User{ID: uuid.New(), Role: domain.RoleCS}, url.Values{
					"code":        {"482913"},
					"location_id": {loc},
				})
			req.Header.Set("HX-Request", "true")
			rec := runRoute(h, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if _, msg := decodeToast(t, rec); msg != i18n.T("en", "tv.pr.noloc") {
				t.Errorf("toast = %q, want %q", msg, i18n.T("en", "tv.pr.noloc"))
			}
			if len(*consume) != 0 {
				t.Error("an invalid branch must not consume the code")
			}
		})
	}
}

// Ruling #8: the CS may name the device; the fallback is "TV · <branch> ·
// <date>" so an unnamed screen is still identifiable on the list.
func TestPortalTVPairLabelFallsBackToBranchAndDate(t *testing.T) {
	prev := tvClock
	tvClock = func() time.Time { return time.Date(2026, 11, 21, 3, 0, 0, 0, time.UTC) } // 10:00 WIB
	t.Cleanup(func() { tvClock = prev })

	h := newRouteTestServer(t)
	swapTVLocations(t, []domain.Location{tvPortalLocation()}, nil)
	swapTVPairingConsume(t, tvPairingFixturePtr(), nil)
	saved := swapTVDeviceCreate(t, nil)
	swapTVPairingLink(t, nil)

	for _, label := range []string{"", "   "} {
		req := tvPortalUserRequest(t, http.MethodPost, "/portal/tv/pair?lang=en",
			&domain.User{ID: uuid.New(), Role: domain.RoleCS}, url.Values{
				"code":        {"482913"},
				"location_id": {tvPortalLocID.String()},
				"label":       {label},
			})
		req.Header.Set("HX-Request", "true")
		if rec := runRoute(h, req); rec.Code != http.StatusNoContent {
			t.Fatalf("label %q: status = %d, want 204", label, rec.Code)
		}
		if (*saved).Label != "TV · Kayu Putih · 21 Nov 2026" {
			t.Errorf("label %q: fallback = %q, want %q", label, (*saved).Label, "TV · Kayu Putih · 21 Nov 2026")
		}
	}
}

// Brute force: ten entries a minute per user get through (and fail on the
// code), the eleventh is refused before it touches the handshake.
func TestPortalTVPairIsRateLimitedPerUser(t *testing.T) {
	h := newRouteTestServer(t)
	swapTVLocations(t, []domain.Location{tvPortalLocation()}, nil)
	consume := swapTVPairingConsume(t, nil, repo.ErrNotFound)
	user := &domain.User{ID: uuid.New(), Role: domain.RoleCS}

	post := func(u *domain.User) *httptest.ResponseRecorder {
		req := tvPortalUserRequest(t, http.MethodPost, "/portal/tv/pair?lang=en", u, url.Values{
			"code":        {"482913"},
			"location_id": {tvPortalLocID.String()},
		})
		req.Header.Set("HX-Request", "true")
		return runRoute(h, req)
	}
	for i := 1; i <= 10; i++ {
		if rec := post(user); rec.Code != http.StatusBadRequest {
			t.Fatalf("attempt %d: status = %d, want 400", i, rec.Code)
		}
	}
	if rec := post(user); rec.Code != http.StatusTooManyRequests {
		t.Errorf("11th attempt: status = %d, want 429", rec.Code)
	}
	// The budget is per staff user, not per IP: a colleague on the front
	// desk's machine keeps working while one account is throttled.
	if rec := post(&domain.User{ID: uuid.New(), Role: domain.RoleCS}); rec.Code != http.StatusBadRequest {
		t.Errorf("other user after the throttle: status = %d, want 400 (budget is per user)", rec.Code)
	}
	if len(*consume) != 11 {
		t.Errorf("repo saw %d attempts, want 11 (10 from the throttled user + 1 from the colleague)", len(*consume))
	}
}

// A repo outage is a 500 toast (the staffFail contract), never a live code
// left half-consumed and never a device.
func TestPortalTVPairRepoFailureToasts(t *testing.T) {
	h := newRouteTestServer(t)
	swapTVLocations(t, []domain.Location{tvPortalLocation()}, nil)
	swapTVPairingConsume(t, nil, fmt.Errorf("db down"))
	saved := swapTVDeviceCreate(t, nil)

	req := tvPortalUserRequest(t, http.MethodPost, "/portal/tv/pair?lang=en",
		&domain.User{ID: uuid.New(), Role: domain.RoleCS}, url.Values{
			"code":        {"482913"},
			"location_id": {tvPortalLocID.String()},
		})
	req.Header.Set("HX-Request", "true")
	rec := runRoute(h, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if kind, msg := decodeToast(t, rec); kind != "error" || msg == "" {
		t.Errorf("toast = (%q, %q), want an error with copy", kind, msg)
	}
	if *saved != nil {
		t.Error("a failed lookup must not mint a device")
	}
}

// If the link step fails, the freshly minted device can never be claimed (the
// TV polls the pairing, not the device), so the handler revokes it instead of
// leaving an orphan credential on the list.
func TestPortalTVPairLinkFailureRevokesOrphan(t *testing.T) {
	h := newRouteTestServer(t)
	swapTVLocations(t, []domain.Location{tvPortalLocation()}, nil)
	swapTVPairingConsume(t, tvPairingFixturePtr(), nil)
	saved := swapTVDeviceCreate(t, nil)
	swapTVPairingLink(t, fmt.Errorf("db down"))
	revoked := swapTVDeviceRevoke(t, nil)

	req := tvPortalUserRequest(t, http.MethodPost, "/portal/tv/pair?lang=en",
		&domain.User{ID: uuid.New(), Role: domain.RoleCS}, url.Values{
			"code":        {"482913"},
			"location_id": {tvPortalLocID.String()},
		})
	req.Header.Set("HX-Request", "true")
	rec := runRoute(h, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if kind, _ := decodeToast(t, rec); kind != "error" {
		t.Error("link failure must toast an error")
	}
	if len(*revoked) != 1 || (*revoked)[0] != (*saved).ID {
		t.Errorf("orphan device revoke calls = %v, want [%s]", *revoked, (*saved).ID)
	}
}

// Revoking a device is an admin correction inside the shared portal (the
// requirePortalAdmin precedent): CS reaches the URL and is refused in-handler,
// the repo is never called.
func TestPortalTVDeviceRevokeIsAdminOnly(t *testing.T) {
	h := newRouteTestServer(t)
	revoked := swapTVDeviceRevoke(t, nil)
	dev := tvPortalDevice()

	req := tvPortalUserRequest(t, http.MethodPost, "/portal/tv/device/"+dev.ID.String()+"/revoke?lang=en",
		&domain.User{ID: uuid.New(), Role: domain.RoleCS}, url.Values{})
	req.Header.Set("HX-Request", "true")
	rec := runRoute(h, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("cs status = %d, want 403", rec.Code)
	}
	if kind, _ := decodeToast(t, rec); kind != "error" {
		t.Error("refused revoke must toast an error")
	}
	if len(*revoked) != 0 {
		t.Errorf("cs revoke must not reach the repo, got %v", *revoked)
	}
}

func TestPortalTVDeviceRevokeRedirectsForAdmin(t *testing.T) {
	h := newRouteTestServer(t)
	revoked := swapTVDeviceRevoke(t, nil)
	dev := tvPortalDevice()

	req := tvPortalUserRequest(t, http.MethodPost, "/portal/tv/device/"+dev.ID.String()+"/revoke?lang=en",
		&domain.User{ID: uuid.New(), Role: domain.RoleAdmin}, url.Values{})
	req.Header.Set("HX-Request", "true")
	rec := runRoute(h, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (htmx redirect)", rec.Code)
	}
	if loc := rec.Header().Get("HX-Redirect"); !strings.HasPrefix(loc, "/portal/tv?success=") {
		t.Errorf("HX-Redirect = %q, want /portal/tv?success=…", loc)
	}
	if len(*revoked) != 1 || (*revoked)[0] != dev.ID {
		t.Errorf("revoked = %v, want [%s]", *revoked, dev.ID)
	}
}

func TestPortalTVDeviceRevokeFailureToasts(t *testing.T) {
	h := newRouteTestServer(t)
	swapTVDeviceRevoke(t, errors.New("db down"))

	req := tvPortalUserRequest(t, http.MethodPost,
		"/portal/tv/device/"+tvPortalDevice().ID.String()+"/revoke",
		&domain.User{ID: uuid.New(), Role: domain.RoleAdmin}, url.Values{})
	req.Header.Set("HX-Request", "true")
	rec := runRoute(h, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if kind, _ := decodeToast(t, rec); kind != "error" {
		t.Error("revoke failure must toast an error")
	}
}

// /portal/tv is a staff page: customers are refused, anonymous visitors are
// sent to login like every other portal route.
func TestPortalTVPageRequiresStaff(t *testing.T) {
	h := newRouteTestServer(t)
	swapTVLocations(t, []domain.Location{tvPortalLocation()}, nil)
	swapTVDeviceList(t, nil, nil)

	if rec := runRoute(h, tvPortalUserRequest(t, http.MethodGet, "/portal/tv",
		&domain.User{ID: uuid.New(), Role: domain.RoleCustomer}, nil)); rec.Code != http.StatusForbidden {
		t.Errorf("customer status = %d, want 403", rec.Code)
	}
	if rec := runRoute(h, httptest.NewRequest(http.MethodGet, "/portal/tv", nil)); rec.Code != http.StatusSeeOther {
		t.Errorf("anonymous status = %d, want 303 to login", rec.Code)
	}
}

// A crafted label longer than the form allows is capped, so one POST cannot
// bloat the device list.
func TestPortalTVPairCapsLabelLength(t *testing.T) {
	h := newRouteTestServer(t)
	swapTVLocations(t, []domain.Location{tvPortalLocation()}, nil)
	swapTVPairingConsume(t, tvPairingFixturePtr(), nil)
	saved := swapTVDeviceCreate(t, nil)
	swapTVPairingLink(t, nil)

	long := strings.Repeat("A", 80)
	req := tvPortalUserRequest(t, http.MethodPost, "/portal/tv/pair?lang=en",
		&domain.User{ID: uuid.New(), Role: domain.RoleCS}, url.Values{
			"code":        {"482913"},
			"location_id": {tvPortalLocID.String()},
			"label":       {long},
		})
	req.Header.Set("HX-Request", "true")
	if rec := runRoute(h, req); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if got := (*saved).Label; len([]rune(got)) != 60 {
		t.Errorf("label length = %d runes, want 60", len([]rune(got)))
	}
}

// A repo outage on the page itself takes the staffFail contract: a toast for
// HTMX, the portal error page otherwise — never a blank page.
func TestPortalTVPageFailureToasts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		locs      []domain.Location
		locsErr   error
		devices   []domain.TVDevice
		devicesEr error
	}{
		{"locations", nil, errors.New("db down"), nil, nil},
		{"devices", []domain.Location{tvPortalLocation()}, nil, nil, errors.New("db down")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRouteTestServer(t)
			swapTVLocations(t, tc.locs, tc.locsErr)
			swapTVDeviceList(t, tc.devices, tc.devicesEr)

			req := tvPortalUserRequest(t, http.MethodGet, "/portal/tv?lang=en",
				&domain.User{ID: uuid.New(), Role: domain.RoleCS}, nil)
			req.Header.Set("HX-Request", "true")
			rec := runRoute(h, req)

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", rec.Code)
			}
			if kind, msg := decodeToast(t, rec); kind != "error" || msg == "" {
				t.Errorf("toast = (%q, %q), want an error with copy", kind, msg)
			}
		})
	}
}

// The other half of that contract: once revoked, the screen is gone from the
// list the CS reads (ListActive filters revoked_at), so a dead device cannot
// be re-paired by mistake. This drives both seams against one in-memory store.
func TestPortalTVRevokedDeviceLeavesTheList(t *testing.T) {
	h := newRouteTestServer(t)
	swapTVLocations(t, []domain.Location{tvPortalLocation()}, nil)
	dev := tvPortalDevice()
	live := map[uuid.UUID]domain.TVDevice{dev.ID: dev}

	prevList, prevRevoke := tvDeviceList, tvDeviceRevoke
	tvDeviceList = func(_ *Server, _ *http.Request) ([]domain.TVDevice, error) {
		out := make([]domain.TVDevice, 0, len(live))
		for _, d := range live {
			if d.RevokedAt == nil {
				out = append(out, d)
			}
		}
		return out, nil
	}
	tvDeviceRevoke = func(_ *Server, _ *http.Request, id uuid.UUID) error {
		d, ok := live[id]
		if !ok {
			return repo.ErrNotFound
		}
		now := time.Now()
		d.RevokedAt = &now
		live[id] = d
		return nil
	}
	t.Cleanup(func() { tvDeviceList, tvDeviceRevoke = prevList, prevRevoke })

	page := func() string {
		rec := runRoute(h, tvPortalUserRequest(t, http.MethodGet, "/portal/tv?lang=en",
			&domain.User{ID: uuid.New(), Role: domain.RoleCS}, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("page status = %d, want 200", rec.Code)
		}
		return rec.Body.String()
	}
	if !contains(page(), "Lobby TV") {
		t.Fatal("a live device is missing from the list")
	}

	req := tvPortalUserRequest(t, http.MethodPost, "/portal/tv/device/"+dev.ID.String()+"/revoke?lang=en",
		&domain.User{ID: uuid.New(), Role: domain.RoleAdmin}, url.Values{})
	req.Header.Set("HX-Request", "true")
	if rec := runRoute(h, req); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, want 204", rec.Code)
	}

	out := page()
	if contains(out, "Lobby TV") {
		t.Error("a revoked device is still listed")
	}
	if !contains(out, "No paired TVs yet.") {
		t.Error("the empty state did not render after the only device was revoked")
	}
}

// The admin page keeps its token section and gains the paired-device section;
// the revoke action points at the admin namespace.
func TestAdminTVPageListsDevices(t *testing.T) {
	h := newRouteTestServer(t)
	swapTVTokenList(t, nil, nil)
	swapTVLocations(t, []domain.Location{tvPortalLocation()}, nil)
	swapTVDeviceList(t, []domain.TVDevice{tvPortalDevice()}, nil)

	rec := runRoute(h, adminRequest(t, http.MethodGet, "/admin/tv?lang=en", domain.RoleAdmin, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	out := rec.Body.String()
	for _, want := range []string{
		"Paired TVs",
		"Lobby TV",
		"Kayu Putih",
		`/admin/tv/device/` + tvPortalDevice().ID.String() + `/revoke`,
	} {
		if !contains(out, want) {
			t.Errorf("admin TV page missing %q", want)
		}
	}
	if contains(out, tvPortalDevice().SecretHash) {
		t.Error("the device secret hash must never reach the admin page")
	}
}

func TestAdminTVDeviceRevokeCallsSeam(t *testing.T) {
	h := newRouteTestServer(t)
	revoked := swapTVDeviceRevoke(t, nil)
	dev := tvPortalDevice()

	rec := runRoute(h, adminRequest(t, http.MethodPost,
		"/admin/tv/device/"+dev.ID.String()+"/revoke", domain.RoleAdmin, url.Values{}))

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "/admin/tv?success=") {
		t.Errorf("Location = %q, want /admin/tv?success=…", loc)
	}
	if len(*revoked) != 1 || (*revoked)[0] != dev.ID {
		t.Errorf("revoked = %v, want [%s]", *revoked, dev.ID)
	}
}

func TestAdminTVDeviceRevokeIsAdminOnly(t *testing.T) {
	h := newRouteTestServer(t)
	revoked := swapTVDeviceRevoke(t, nil)

	rec := runRoute(h, adminRequest(t, http.MethodPost,
		"/admin/tv/device/"+tvPortalDevice().ID.String()+"/revoke", domain.RoleCS, url.Values{}))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cs status = %d, want 403", rec.Code)
	}
	if len(*revoked) != 0 {
		t.Errorf("cs revoke must not reach the repo, got %v", *revoked)
	}
}

// An unknown device id is a stale page, not a 500.
func TestAdminTVDeviceRevokeRejectsBadID(t *testing.T) {
	h := newRouteTestServer(t)
	revoked := swapTVDeviceRevoke(t, nil)

	req := adminRequest(t, http.MethodPost, "/admin/tv/device/not-a-uuid/revoke",
		domain.RoleAdmin, url.Values{})
	req.Header.Set("HX-Request", "true")
	rec := runRoute(h, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if len(*revoked) != 0 {
		t.Errorf("bad id must not reach the repo, got %v", *revoked)
	}
}
