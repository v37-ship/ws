package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"spa-booking/internal/config"
	"spa-booking/internal/domain"
	"spa-booking/internal/http/middleware"

	"github.com/go-chi/chi/v5"
)

// newRouteTestServer wires the real router so the test pins the URL mapping,
// not just a handler body: retiring a duplicate page means the old path must
// keep answering, and only a router-level assertion proves it still does.
func newRouteTestServer(t *testing.T) http.Handler {
	t.Helper()
	s, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	r := chi.NewRouter()
	s.Routes(r)
	return r
}

// asUser runs one GET with the given role already resolved in the context,
// which is what middleware.LoadUser does in production.
func asUser(h http.Handler, path string, role domain.Role) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserKey, &domain.User{Role: role}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The legacy admin bookings table is strictly dominated by the paginated,
// filterable portal list; the old URL must forward there instead of rendering
// a second source of truth.
func TestAdminBookingsRedirectsToPortal(t *testing.T) {
	h := newRouteTestServer(t)
	rec := asUser(h, "/admin/bookings", domain.RoleAdmin)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/portal/bookings" {
		t.Errorf("Location = %q, want /portal/bookings", got)
	}
}

// The redirect lives inside the admin-only group, exactly like the
// /admin/services forwarder: retiring the page must not widen who may reach
// the admin tree (the portal target stays staff-gated on its own).
func TestAdminBookingsRedirectStaysAdminOnly(t *testing.T) {
	h := newRouteTestServer(t)
	if rec := asUser(h, "/admin/bookings", domain.RoleCS); rec.Code != http.StatusForbidden {
		t.Errorf("cs status = %d, want 403", rec.Code)
	}
}

// /admin/payments used to render a bare centered <main>, so an admin arriving
// from the portal lost the sidebar. It now shares the portal shell (and the
// sticky-header drawer) exactly like /admin/cs.
func TestAdminPaymentsKeepsPortalNav(t *testing.T) {
	s, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/payments", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserKey,
		&domain.User{Role: domain.RoleAdmin, FullName: "Admin"}))
	s.render(rec, req, "admin_payments.html", pageData{"QRISImage": "/static/img/qris-wangga.jpeg"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	out := rec.Body.String()
	for _, want := range []string{
		`class="portal-shell"`,   // 2-column portal chrome
		`id="portal-side"`,       // the sidebar itself
		`href="/admin/payments"`, // admin group of the nav is live, not an empty shell
		`theme-btn nav-toggle`,   // HasDrawer derived for admin_payments
	} {
		if !strings.Contains(out, want) {
			t.Errorf("admin_payments page missing %q", want)
		}
	}
}
