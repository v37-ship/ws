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

	"spa-booking/internal/domain"
	"spa-booking/internal/http/middleware"
	"spa-booking/internal/repo"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// waOutboxAdminFake drives the admin page and its retry action without a
// database: the page reads one recent row per status and the summary counters,
// the action reports whether a row changed (or a DB error).
type waOutboxAdminFake struct {
	recent  []repo.WaOutboxItem
	pending int
	failed  int
	changed bool
	err     error
	retried []uuid.UUID
}

func (f *waOutboxAdminFake) ListUpcoming(context.Context, string, int) ([]repo.WaOutboxItem, error) {
	return nil, nil
}

func (f *waOutboxAdminFake) ListRecent(context.Context, int) ([]repo.WaOutboxItem, error) {
	return f.recent, nil
}

func (f *waOutboxAdminFake) ListByBooking(context.Context, uuid.UUID, int) ([]repo.WaOutboxItem, error) {
	return nil, nil
}

func (f *waOutboxAdminFake) RetryFailed(_ context.Context, id uuid.UUID) (bool, error) {
	f.retried = append(f.retried, id)
	return f.changed, f.err
}

func (f *waOutboxAdminFake) QueueSummary(context.Context) (int, int, error) {
	return f.pending, f.failed, nil
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
}

func TestAdminRetryWaOutboxNotRetryable(t *testing.T) {
	s := newWaTestServer(t, slog.Default())
	fake := &waOutboxAdminFake{changed: false}
	s.waOutbox = fake

	// A cancelled row is a deliberate withdrawal: the repo refuses it and the
	// handler must land on the same notice as an already-sent or unknown row.
	for _, tc := range []struct {
		name string
		id   uuid.UUID
	}{
		{"cancelled", waSweepCancelledID},
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

// TestAdminWhatsAppRetryFormOnlyOnFailedRows renders both branches in both
// languages: the counters line, the retry form on failed rows only (a cancelled
// row is a withdrawal, not a failure), and no raw i18n key leaking.
func TestAdminWhatsAppRetryFormOnlyOnFailedRows(t *testing.T) {
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
		// Exactly the failed row carries a form.
		if got := strings.Count(page, `action="/admin/whatsapp/outbox/`); got != 1 {
			t.Errorf("%s: retry forms = %d, want 1 (failed only)", lang, got)
		}
		want := `action="/admin/whatsapp/outbox/` + waSweepFailedID.String() + `/retry"`
		if !strings.Contains(page, want) {
			t.Errorf("%s: missing the retry form for the failed row", lang)
		}
		// Sent and cancelled rows must not gain one; the cancelled row keeps
		// its status text with no action.
		for _, id := range []uuid.UUID{waSweepSentID, waSweepCancelledID} {
			if strings.Contains(page, id.String()+`/retry`) {
				t.Errorf("%s: row %s must not be retryable", lang, id)
			}
		}
		if !strings.Contains(page, "cancelled") {
			t.Errorf("%s: the cancelled row lost its status text", lang)
		}
	}
}
