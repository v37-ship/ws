package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"spa-booking/internal/config"
)

func newFailTestServer(t *testing.T) *Server {
	t.Helper()
	s, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return s
}

// decodeToast pulls the toast payload out of an HX-Trigger header so the test
// asserts what app.js consumes, not just that some header exists.
func decodeToast(t *testing.T, rec *httptest.ResponseRecorder) (kind, message string) {
	t.Helper()
	raw := rec.Header().Get("HX-Trigger")
	if raw == "" {
		t.Fatalf("missing HX-Trigger header")
	}
	var events map[string]struct {
		Kind    string `json:"kind"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(raw), &events); err != nil {
		t.Fatalf("HX-Trigger is not JSON (%q): %v", raw, err)
	}
	toast, ok := events["toast"]
	if !ok {
		t.Fatalf("HX-Trigger has no toast event: %q", raw)
	}
	return toast.Kind, toast.Message
}

// An HTMX control that hits a stale CSRF token must keep its status code and
// push an error toast; the app's HTMX config ignores non-2xx swaps, so a bare
// http.Error body never reaches the screen (the classic dead button).
func TestStaffFailHTMXToast(t *testing.T) {
	s := newFailTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/portal/promos", strings.NewReader(""))
	req.Header.Set("HX-Request", "true")

	const msg = "Your session expired. Refresh the page and try again."
	s.staffFail(rec, req, http.StatusBadRequest, msg)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 preserved", rec.Code)
	}
	if body := rec.Body.String(); body != "" {
		t.Errorf("HTMX failure must not write a body, got %q", body)
	}
	kind, got := decodeToast(t, rec)
	if kind != "error" {
		t.Errorf("toast kind = %q, want error", kind)
	}
	if got != msg {
		t.Errorf("toast message = %q, want %q", got, msg)
	}
}

// A plain form post (no HTMX) must not dump bare text into the browser: it
// gets the portal shell, the translated title, the message and a way back.
func TestStaffFailRendersPortalPage(t *testing.T) {
	s := newFailTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/portal/promos", strings.NewReader(""))

	const msg = "Only an admin can do that."
	s.staffFail(rec, req, http.StatusForbidden, msg)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 page render", rec.Code)
	}
	out := rec.Body.String()
	for _, want := range []string{
		`id="portal-tabs"`, // portal chrome, not a naked error page
		msg,
		`href="/portal"`,              // way back
		`lang="id"`,                   // default language when nothing is negotiated
		`theme-btn nav-toggle`,        // HasDrawer derived from the portal_ prefix
		"Tindakan itu belum berhasil", // translated title (default ID)
	} {
		if !contains(out, want) {
			t.Errorf("portal_error page missing %q", want)
		}
	}
	if contains(out, "p.err.t") || contains(out, "invalid request") {
		t.Error("portal_error page leaks a raw key or the old bare error body")
	}
}

// The CSRF guard on a real portal POST handler is the first user of the
// helper: an HTMX request with a bad token answers a toast, not bare text.
func TestPortalCreatePromoCSRFToast(t *testing.T) {
	s := newFailTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/portal/promos",
		strings.NewReader(url.Values{"code": {"SEHAT10"}, "kind": {"percent"}, "value": {"10"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")

	s.PortalCreatePromo(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if body := rec.Body.String(); body != "" {
		t.Errorf("HTMX failure must not write a body, got %q", body)
	}
	kind, msg := decodeToast(t, rec)
	if kind != "error" {
		t.Errorf("toast kind = %q, want error", kind)
	}
	if strings.TrimSpace(msg) == "" {
		t.Error("toast message is empty")
	}
}

// The same guard on a plain form post renders the portal page at 200.
func TestPortalCreatePromoCSRFPage(t *testing.T) {
	s := newFailTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/portal/promos", strings.NewReader(""))

	s.PortalCreatePromo(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 portal error page", rec.Code)
	}
	out := rec.Body.String()
	if !contains(out, `id="portal-tabs"`) || !contains(out, `id="portal-error"`) {
		t.Error("plain form CSRF failure must render the portal error page")
	}
}
