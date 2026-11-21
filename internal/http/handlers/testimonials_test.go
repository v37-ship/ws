package handlers

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"spa-booking/internal/config"

	"github.com/go-chi/chi/v5"
)

// newTestRouter builds the production route table with nil services: enough to
// prove which middleware guards a route, without touching the database.
func newTestRouter(t *testing.T) http.Handler {
	t.Helper()
	s, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	r := chi.NewRouter()
	s.Routes(r)
	return r
}

// The counter QR card sends logged-out customers here, so the review form must
// render for guests instead of bouncing to /login.
func TestTestimonialNewFormServesGuests(t *testing.T) {
	h := newTestRouter(t)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/testimonials/new", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /testimonials/new = %d, want 200 (location %q)", rec.Code, rec.Header().Get("Location"))
	}
	if !strings.Contains(rec.Body.String(), `action="/testimonials/new"`) {
		t.Error("guest review form not rendered")
	}
}

// A guest POST must reach the CSRF check (400), never the login redirect the
// old RequireAuth guard produced (303 to /login?next=/testimonials/new).
func TestCreateTestimonialGuestPostHasNoLoginRedirect(t *testing.T) {
	h := newTestRouter(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/testimonials/new", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ServeHTTP(rec, req)
	if loc := rec.Header().Get("Location"); strings.Contains(loc, "/login") {
		t.Fatalf("guest POST redirected to login: %q", loc)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("guest POST without CSRF = %d, want 400", rec.Code)
	}
}

// The open route must still be throttled: 30 submissions per minute per client,
// with the form read keeping its own bucket so browsing cannot lock out a send.
func TestCreateTestimonialGuestIsRateLimited(t *testing.T) {
	h := newTestRouter(t)
	const guest = "203.0.113.50:4444"
	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/testimonials/new", strings.NewReader(""))
		req.RemoteAddr = guest
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	for i := 0; i < 30; i++ {
		if rec := post(); rec.Code != http.StatusBadRequest {
			t.Fatalf("submit %d = %d, want 400", i+1, rec.Code)
		}
	}
	if rec := post(); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("submit 31 = %d, want 429", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "/testimonials/new", nil)
	req.RemoteAddr = guest
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("form after 31 submits = %d, want 200", rec.Code)
	}
}
