package handlers

import (
	"context"
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

// A bare /tv (no ?t=) is the common case of someone typing the address or a
// stale bookmark: it must answer a minimal page and never touch the database.
func TestTVRouteWithoutTokenShowsInvalidPage(t *testing.T) {
	h := newRouteTestServer(t)
	calls := ""
	swapTVActive(t, nil, repo.ErrNotFound, &calls)

	rec := runRoute(h, httptest.NewRequest(http.MethodGet, "/tv?lang=en", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (minimal page, not an error)", rec.Code)
	}
	out := rec.Body.String()
	for _, want := range []string{"Display not available", "No schedule data is shown here."} {
		if !contains(out, want) {
			t.Errorf("invalid page missing %q", want)
		}
	}
	if calls != "" {
		t.Errorf("no token must not reach the lookup seam, got %q", calls)
	}
	if contains(out, "/tv?t=") || contains(out, tvTestToken) {
		t.Error("invalid page must not echo a kiosk link or token")
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

// The admin page must show the full link (for typing on the TV) plus the
// rotating-layout variant, and a revoke control per active token.
func TestAdminTVPageShowsURLs(t *testing.T) {
	h := newRouteTestServer(t)
	tok := tvFixture()
	swapTVTokenList(t, []domain.TVToken{tok}, nil)
	swapTVLocations(t, []domain.Location{{ID: tok.LocationID, Name: "Kayu Putih", Active: true}}, nil)

	rec := runRoute(h, adminRequest(t, http.MethodGet, "/admin/tv?lang=en", domain.RoleAdmin, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	out := rec.Body.String()
	for _, want := range []string{
		"Lobby TV",
		"/tv?t=" + tvTestToken,
		"layout=rotate",
		"/admin/tv/revoke/" + tok.ID.String(),
		`action="/admin/tv/generate"`,
		"Kayu Putih",
		"Active links",
	} {
		if !contains(out, want) {
			t.Errorf("admin TV page missing %q", want)
		}
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
