package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/http/middleware"
	"spa-booking/internal/repo"
	"spa-booking/internal/service"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// waOutboxAdminFake drives the admin page and its retry action without a
// database: the page reads one recent row per status and the summary counters,
// the action reports whether a row changed (or a DB error).
type waOutboxAdminFake struct {
	recent   []repo.WaOutboxItem
	failed   []repo.WaOutboxItem
	pending  int
	failedN  int
	queueErr error
	changed  bool
	err      error
	retried  []uuid.UUID
	retryAt  []time.Time
	views    []string
}

func (f *waOutboxAdminFake) ListUpcoming(context.Context, string, int) ([]repo.WaOutboxItem, error) {
	return nil, nil
}

func (f *waOutboxAdminFake) ListRecent(context.Context, int) ([]repo.WaOutboxItem, error) {
	f.views = append(f.views, "recent")
	return f.recent, nil
}

func (f *waOutboxAdminFake) ListFailed(context.Context, int) ([]repo.WaOutboxItem, error) {
	f.views = append(f.views, "failed")
	return f.failed, nil
}

func (f *waOutboxAdminFake) ListByBooking(context.Context, uuid.UUID, int) ([]repo.WaOutboxItem, error) {
	return nil, nil
}

func (f *waOutboxAdminFake) RetryFailed(_ context.Context, id uuid.UUID, at time.Time) (bool, error) {
	f.retried = append(f.retried, id)
	f.retryAt = append(f.retryAt, at)
	return f.changed, f.err
}

func (f *waOutboxAdminFake) QueueSummary(context.Context) (int, int, error) {
	return f.pending, f.failedN, f.queueErr
}

// waRetryPost drives the handler directly, with chi's URL param and the
// resolved user already in the context (what the router does in production).
func waRetryPost(t *testing.T, s *Server, user *domain.User, id string, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{}
	if csrf {
		form.Set("csrf", "token")
	}
	req := httptest.NewRequest(http.MethodPost, "/admin/whatsapp/outbox/"+id+"/retry?lang=en", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if csrf {
		req.AddCookie(&http.Cookie{Name: "csrf", Value: "token"})
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	if user != nil {
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserKey, user))
	}
	rec := httptest.NewRecorder()
	s.AdminRetryWaOutbox(rec, req)
	return rec
}

func TestAdminRetryWaOutboxSuccess(t *testing.T) {
	s := newWaTestServer(t, slog.Default())
	fake := &waOutboxAdminFake{changed: true}
	s.waOutbox = fake
	id := uuid.New()

	rec := waRetryPost(t, s, &domain.User{ID: uuid.New(), Role: domain.RoleAdmin}, id.String(), true)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "success=") || strings.Contains(loc, "error=") {
		t.Fatalf("Location = %q, want a success flag", loc)
	}
	if want := url.QueryEscape("Message queued for another attempt"); !strings.Contains(loc, want) {
		t.Errorf("Location = %q, want the localized success message %q", loc, want)
	}
	if len(fake.retried) != 1 || fake.retried[0] != id {
		t.Errorf("retried = %v, want [%s]", fake.retried, id)
	}
	// Ruling R15: the retry is scheduled inside the 07:00-20:00 WIB window.
	if len(fake.retryAt) != 1 {
		t.Fatalf("retryAt = %v, want one schedule time", fake.retryAt)
	}
	wib := time.FixedZone("WIB", 7*3600)
	if h := fake.retryAt[0].In(wib).Hour(); h < 7 || h >= 20 {
		t.Errorf("retry scheduled at %v WIB, want inside 07:00-20:00", fake.retryAt[0].In(wib))
	}
}

func TestAdminRetryWaOutboxNotRetryable(t *testing.T) {
	s := newWaTestServer(t, slog.Default())
	fake := &waOutboxAdminFake{changed: false}
	s.waOutbox = fake

	// A cancelled row is a deliberate withdrawal and a failed row whose booking
	// has since closed is stale: the repo refuses both, and the handler must
	// land on the same notice as an already-sent or unknown row.
	for _, tc := range []struct {
		name string
		id   uuid.UUID
	}{
		{"cancelled", waSweepCancelledID},
		{"stale (booking closed)", waSweepClosedID},
		{"already sent", waSweepSentID},
		{"unknown", uuid.New()},
	} {
		rec := waRetryPost(t, s, &domain.User{ID: uuid.New(), Role: domain.RoleAdmin}, tc.id.String(), true)

		if rec.Code != http.StatusSeeOther {
			t.Fatalf("%s: status = %d, want 303", tc.name, rec.Code)
		}
		loc := rec.Header().Get("Location")
		if !strings.Contains(loc, "error=") || strings.Contains(loc, "success=") {
			t.Fatalf("%s: Location = %q, want an error flag", tc.name, loc)
		}
		if want := url.QueryEscape("That message cannot be retried"); !strings.Contains(loc, want) {
			t.Errorf("%s: Location = %q, want the localized refusal %q", tc.name, loc, want)
		}
		// The repo was consulted (it owns the status predicate); it refused.
		if len(fake.retried) == 0 || fake.retried[len(fake.retried)-1] != tc.id {
			t.Errorf("%s: retried = %v, want the refused id consulted", tc.name, fake.retried)
		}
	}
}

func TestAdminRetryWaOutboxBadID(t *testing.T) {
	s := newWaTestServer(t, slog.Default())
	fake := &waOutboxAdminFake{changed: true}
	s.waOutbox = fake

	rec := waRetryPost(t, s, &domain.User{ID: uuid.New(), Role: domain.RoleAdmin}, "not-a-uuid", true)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if loc := rec.Header().Get("Location"); !strings.Contains(loc, "error=") {
		t.Errorf("Location = %q, want the not-retryable flag", loc)
	}
	if len(fake.retried) != 0 {
		t.Errorf("retried = %v, want no repository call for an unparseable id", fake.retried)
	}
}

func TestAdminRetryWaOutboxCSRF(t *testing.T) {
	s := newWaTestServer(t, slog.Default())
	fake := &waOutboxAdminFake{changed: true}
	s.waOutbox = fake

	rec := waRetryPost(t, s, &domain.User{ID: uuid.New(), Role: domain.RoleAdmin}, uuid.NewString(), false)

	// Plain form post: the canonical staffFail page at 200, not a bare 400 line.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 portal error page", rec.Code)
	}
	out := rec.Body.String()
	if !strings.Contains(out, `id="portal-error"`) || !strings.Contains(out, failSession) {
		t.Error("a stale CSRF token must render the portal error page")
	}
	if len(fake.retried) != 0 {
		t.Errorf("retried = %v, want no repository call without a CSRF token", fake.retried)
	}
}

func TestAdminRetryWaOutboxDBErrorLogs(t *testing.T) {
	var logs waLogCapture
	logger := slog.New(&logs)
	s := newWaTestServer(t, logger)
	s.waOutbox = &waOutboxAdminFake{err: errors.New("db down")}

	rec := waRetryPost(t, s, &domain.User{ID: uuid.New(), Role: domain.RoleAdmin}, uuid.NewString(), true)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 portal error page", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), failWaRetry) {
		t.Error("a DB error must surface the delivery-action notice")
	}
	records := logs.all()
	if len(records) != 1 || records[0].Message != "wa outbox retry" {
		t.Fatalf("logs = %v, want one 'wa outbox retry' record", records)
	}
	if records[0].Level != slog.LevelError {
		t.Errorf("log level = %s, want ERROR", records[0].Level)
	}
}

// TestAdminRetryWaOutboxAdminOnly pins the route gating: the retry is inside
// the admin group, so a CS account is refused before the handler runs.
func TestAdminRetryWaOutboxAdminOnly(t *testing.T) {
	h := newRouteTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/admin/whatsapp/outbox/"+uuid.NewString()+"/retry", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserKey,
		&domain.User{Role: domain.RoleCS}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cs status = %d, want 403", rec.Code)
	}
}

// TestWaRetryableMirrorsRepoGuard pins the Go side of the shared predicate
// (rulings R6/R16): only failed rows, never a closed booking, and a reminder
// only while its visit is still ahead — while a review row on a completed visit
// stays retryable.
func TestWaRetryableMirrorsRepoGuard(t *testing.T) {
	now := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	bookingID := uuid.New()
	remind := func(s string) *string { return &s }
	cases := []struct {
		name string
		item repo.WaOutboxItem
		want bool
	}{
		{"sent", repo.WaOutboxItem{Status: "sent", BookingID: &bookingID, BookingStatus: "confirmed", BookingStart: now.Add(time.Hour)}, false},
		{"no booking", repo.WaOutboxItem{Status: "failed"}, true},
		{"confirmed future reminder", repo.WaOutboxItem{Status: "failed", BookingID: &bookingID, BookingStatus: "confirmed", BookingStart: now.Add(time.Hour), DedupeKey: remind("remind:x")}, true},
		{"completed reminder", repo.WaOutboxItem{Status: "failed", BookingID: &bookingID, BookingStatus: "completed", BookingStart: now.Add(-time.Hour), DedupeKey: remind("remind:x")}, false},
		{"past reminder", repo.WaOutboxItem{Status: "failed", BookingID: &bookingID, BookingStatus: "confirmed", BookingStart: now.Add(-time.Minute), DedupeKey: remind("remind:x")}, false},
		{"closed booking", repo.WaOutboxItem{Status: "failed", BookingID: &bookingID, BookingStatus: "cancelled", BookingStart: now.Add(time.Hour)}, false},
		{"completed review", repo.WaOutboxItem{Status: "failed", BookingID: &bookingID, BookingStatus: "completed", BookingStart: now.Add(-time.Hour), DedupeKey: remind("review:x")}, true},
	}
	for _, tc := range cases {
		if got := waRetryable(tc.item, now); got != tc.want {
			t.Errorf("%s: waRetryable = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// waAdminSettingsFake satisfies the settings surface the page reads.
type waAdminSettingsFake struct{}

func (waAdminSettingsFake) Get(context.Context) (*domain.WaSettings, error) {
	return &domain.WaSettings{}, nil
}
func (waAdminSettingsFake) Save(context.Context, domain.WaSettings, uuid.UUID) error { return nil }
func (waAdminSettingsFake) ListOverrides(context.Context) (map[string]string, error) {
	return map[string]string{}, nil
}
func (waAdminSettingsFake) ListOverrideMeta(context.Context) (map[string]domain.WaOverrideMeta, error) {
	return map[string]domain.WaOverrideMeta{}, nil
}
func (waAdminSettingsFake) SetOverride(context.Context, string, string, uuid.UUID) error { return nil }
func (waAdminSettingsFake) DeleteOverride(context.Context, string) error                 { return nil }

// TestAdminWhatsAppOutboxView pins the ?outbox=failed switch: it reads the
// failed list instead of the recent window and keeps the default route on
// recent. The counters' zero must read "—" rather than a confident zero when
// the summary read fails.
func TestAdminWhatsAppOutboxView(t *testing.T) {
	render := func(t *testing.T, query string, fake *waOutboxAdminFake) *httptest.ResponseRecorder {
		t.Helper()
		s := newWaTestServer(t, slog.Default())
		s.waSettings = service.NewWaSettingsService(waAdminSettingsFake{}, service.WaConfig{}, slog.Default())
		s.waOutbox = fake
		req := httptest.NewRequest(http.MethodGet, "/admin/whatsapp?lang=en"+query, nil)
		rec := httptest.NewRecorder()
		s.AdminWhatsApp(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		return rec
	}

	recentRow := repo.WaOutboxItem{ID: waSweepSentID, Phone: "6281200000004", TemplateKey: "booking_confirm", Status: "sent"}
	failedRow := repo.WaOutboxItem{ID: waSweepFailedID, Phone: "6281200000001", TemplateKey: "test", Status: "failed"}

	fake := &waOutboxAdminFake{recent: []repo.WaOutboxItem{recentRow}, failed: []repo.WaOutboxItem{failedRow}}
	render(t, "", fake)
	if len(fake.views) != 1 || fake.views[0] != "recent" {
		t.Errorf("default view reads = %v, want [recent]", fake.views)
	}

	fake = &waOutboxAdminFake{recent: []repo.WaOutboxItem{recentRow}, failed: []repo.WaOutboxItem{failedRow}}
	rec := render(t, "&outbox=failed", fake)
	if len(fake.views) != 1 || fake.views[0] != "failed" {
		t.Errorf("failed view reads = %v, want [failed]", fake.views)
	}
	if !strings.Contains(rec.Body.String(), failedRow.ID.String()) {
		t.Error("failed view must render the failed row")
	}

	fake = &waOutboxAdminFake{queueErr: errors.New("db down")}
	rec = render(t, "", fake)
	if !strings.Contains(rec.Body.String(), "—") {
		t.Error("a failed summary must render a dash, not a confident zero")
	}
}

// TestAdminWhatsAppRetryFormOnlyOnLiveFailedRows renders every branch in both
// languages: the counters line, the retry form on failed rows whose booking is
// live (or absent) only — a cancelled row is a withdrawal and a failed row
// whose booking closed is stale — and no raw i18n key leaking.
func TestAdminWhatsAppRetryFormOnlyOnLiveFailedRows(t *testing.T) {
	s := newWaTestServer(t, slog.Default())
	out := func(lang string) string {
		return waTestRender(t, s, lang, waTemplatesSweep())
	}

	for _, lang := range []string{"en", "id"} {
		page := out(lang)
		counter, action, retry := "pending 2 · failed 1", "Action", ">Retry<"
		if lang == "id" {
			counter, action, retry = "menunggu 2 · gagal 1", "Aksi", ">Kirim ulang<"
		}
		if !strings.Contains(page, counter) {
			t.Errorf("%s: missing the queue counters %q", lang, counter)
		}
		if !strings.Contains(page, action) {
			t.Errorf("%s: missing the action column header %q", lang, action)
		}
		if !strings.Contains(page, retry) {
			t.Errorf("%s: missing the retry button label %q", lang, retry)
		}
		if strings.Contains(page, "a.wa.") {
			t.Errorf("%s: raw i18n key leaked", lang)
		}
		// The two live failed rows (live booking + no booking) carry a form.
		if got := strings.Count(page, `action="/admin/whatsapp/outbox/`); got != 2 {
			t.Errorf("%s: retry forms = %d, want 2 (live failed rows only)", lang, got)
		}
		for _, id := range []uuid.UUID{waSweepFailedID, waSweepNoBookingID} {
			want := `action="/admin/whatsapp/outbox/` + id.String() + `/retry"`
			if !strings.Contains(page, want) {
				t.Errorf("%s: missing the retry form for %s", lang, id)
			}
		}
		// A failed row whose booking closed, a reminder whose visit already
		// happened, a sent row and a cancelled row must not gain one; the rows
		// keep their status text regardless.
		for _, id := range []uuid.UUID{waSweepClosedID, waSweepPastRemind, waSweepSentID, waSweepCancelledID} {
			if strings.Contains(page, id.String()+`/retry`) {
				t.Errorf("%s: row %s must not be retryable", lang, id)
			}
		}
		for _, status := range []string{"failed", "cancelled", "sent"} {
			if !strings.Contains(page, status) {
				t.Errorf("%s: the %s status text is missing", lang, status)
			}
		}
		// The retry control confirms with the template key and the masked
		// target, never the full number (ruling R15).
		confirm := "Retry sending booking_remind → …0001?"
		if lang == "id" {
			confirm = "Kirim ulang pesan booking_remind → …0001?"
		}
		if !strings.Contains(page, confirm) {
			t.Errorf("%s: retry confirm must name the template and masked target, want %q", lang, confirm)
		}
		// The built-in copy card explains the empty textarea.
		if lang == "en" && !strings.Contains(page, "The empty box uses the built-in copy") {
			t.Error("en: missing the built-in copy note")
		}
		if lang == "id" && !strings.Contains(page, "Kotak kosong memakai teks bawaan") {
			t.Error("id: missing the built-in copy note")
		}
		only, all := "Failed only", "All recent"
		if lang == "id" {
			only, all = "Hanya gagal", "Semua terbaru"
		}
		if !strings.Contains(page, only) || !strings.Contains(page, all) {
			t.Errorf("%s: missing the outbox view toggle (%q / %q)", lang, only, all)
		}
	}
}
