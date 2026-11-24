package handlers

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"spa-booking/internal/config"
	"spa-booking/internal/domain"
	"spa-booking/internal/http/middleware"
	"spa-booking/internal/repo"
	"spa-booking/internal/service"
	"spa-booking/internal/whatsapp"

	"github.com/google/uuid"
)

func TestParseWaSettingsFormTriState(t *testing.T) {
	values := url.Values{
		"reminder_enabled": {"off"},
		"customer_enabled": {"on"},
		"remind_hour":      {"9"},
		"group_mode":       {"all"},
		"staff_group_jid":  {"120363-429@g.us"},
		"review_url":       {"https://g.page/r/x/review"},
		// outbound_enabled / digest_enabled / digest_hour left blank = env
	}
	r := httptest.NewRequest("POST", "/admin/whatsapp", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	row, err := parseWaSettingsForm(r)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if row.ReminderEnabled == nil || *row.ReminderEnabled {
		t.Error("reminder_enabled off not parsed")
	}
	if row.CustomerEnabled == nil || !*row.CustomerEnabled {
		t.Error("customer_enabled on not parsed")
	}
	if row.RemindHour == nil || *row.RemindHour != 9 {
		t.Error("remind_hour not parsed")
	}
	if row.GroupMode == nil || *row.GroupMode != "all" {
		t.Error("group_mode not parsed")
	}
	if row.OutboundEnabled != nil {
		t.Error("blank field must stay nil (from env)")
	}
	if row.DigestHour != nil {
		t.Error("blank hour must stay nil (from env)")
	}
}

func TestParseWaSettingsFormRejectsBadHour(t *testing.T) {
	values := url.Values{"remind_hour": {"late"}}
	r := httptest.NewRequest("POST", "/admin/whatsapp", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if _, err := parseWaSettingsForm(r); err == nil {
		t.Error("a non-numeric hour must be rejected")
	}
}

// TestWaTemplateViewSegments pins the counter wiring: it describes the body as
// typed (ruling R1), so placeholders count as typed rather than sample-expanded.
func TestWaTemplateViewSegments(t *testing.T) {
	v := waTemplateViewFor("booking_confirm", &domain.WaOverrideMeta{Body: "Halo {name}"})
	if v.Encoding != "GSM-7" || v.Chars != 11 || v.Segments != 1 {
		t.Errorf("typed override = (%s, %d, %d), want (GSM-7, 11, 1)",
			v.Encoding, v.Chars, v.Segments)
	}
	if v.Preview != "Halo Bunda Sari" {
		t.Errorf("preview = %q, want the sample-expanded body", v.Preview)
	}

	// No override: the built-in copy is the counter's body. Every default
	// carries at least one emoji, so it is UCS-2 and never zero.
	def := waTemplateViewFor("booking_remind", nil)
	if def.Encoding != "UCS-2" || def.Chars == 0 || def.Segments == 0 {
		t.Errorf("default view = (%s, %d, %d), want UCS-2 with content",
			def.Encoding, def.Chars, def.Segments)
	}
	if def.Override != "" || def.Custom {
		t.Errorf("no override expected, got %q (custom=%v)", def.Override, def.Custom)
	}
}

// waAuditWhen is the fixed edit stamp these tests render: 03:04 UTC is 10:04
// WIB, so a server-local render would visibly disagree in a UTC test runner.
var waAuditWhen = time.Date(2026, 11, 24, 3, 4, 0, 0, time.UTC)

// waTestRender renders the admin WhatsApp page with the given template cards.
func waTestRender(t *testing.T, s *Server, lang string, views []waTemplateView) string {
	t.Helper()
	data := pageData{
		"Lang": lang,
		"Contact": pageData{
			"WhatsApp": "62800", "Email": "a@b.c",
			"Address": "addr", "Hours": "hours", "Maps": "https://maps.example/",
			"Instagram": "https://instagram.example/", "TikTok": "https://tiktok.example/", "Facebook": "https://facebook.example/",
		},
		"Settings":  service.WaConfig{},
		"Raw":       &domain.WaSettings{},
		"Defaults":  service.WaConfig{},
		"Conn":      "mock",
		"Templates": views,
		// One row per retry branch (failed, sent, cancelled) plus the counters
		// so every page render covers the outbox panel's real shape.
		"Recent":       waQueueViews(),
		"OutboxFailed": false,
		"QueuePending": 2,
		"QueueFailed":  1,
		"QueueOK":      true,
	}
	var buf bytes.Buffer
	if err := s.tpl.ExecuteTemplate(&buf, "admin_whatsapp.html", data); err != nil {
		t.Fatalf("execute admin_whatsapp.html: %v", err)
	}
	return buf.String()
}

// waTemplatesSweep feeds the generic page sweeps both audit branches (edited
// and built-in) so the copy line is translated in both languages. The Default
// and Preview samples stay emoji-free: the real built-in copy carries emoji
// (WhatsApp content, exempt from the UI-chrome scan) and the sweep only ever
// showed a placeholder sample.
func waTemplatesSweep() []waTemplateView {
	views := []waTemplateView{
		waTemplateViewFor("booking_remind", &domain.WaOverrideMeta{
			Body: "Halo Bunda", UpdatedByName: "Bu Sari", UpdatedAt: waAuditWhen,
		}),
		waTemplateViewFor("booking_confirm", nil),
	}
	for i := range views {
		views[i].Default = "Halo Bunda"
		views[i].Preview = "Halo Bunda"
	}
	return views
}

// waSweep*IDs are fixed ids so a test can name the row it expects (or does not
// expect) a retry form for; random ids would differ between sweep calls.
var (
	waSweepFailedID    = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	waSweepSentID      = uuid.MustParse("22222222-2222-4222-8222-222222222222")
	waSweepCancelledID = uuid.MustParse("33333333-3333-4333-8333-333333333333")
	waSweepNoBookingID = uuid.MustParse("44444444-4444-4444-8444-444444444444")
	waSweepClosedID    = uuid.MustParse("55555555-5555-4555-8555-555555555555")
	waSweepPastRemind  = uuid.MustParse("66666666-6666-4666-8666-666666666666")
)

// waQueueSweep returns one recent row per outbox branch (failed with a live
// booking, failed with no booking, failed whose booking closed, failed past
// reminder, sent, cancelled), stamped like the copy audit fixtures so the sweep
// renders deterministically. Only the failed rows with a live (or no) booking
// are retryable; cancelled is a withdrawal, a closed booking is stale, and a
// reminder whose visit already happened is refused (ruling R16).
func waQueueSweep() []repo.WaOutboxItem {
	booked := uuid.MustParse("77777777-7777-4777-8777-777777777777")
	remind := "remind:" + booked.String()
	future := waAuditWhen.Add(24 * time.Hour)
	past := waAuditWhen.Add(-time.Hour)
	return []repo.WaOutboxItem{
		{ID: waSweepFailedID, Phone: "6281200000001", TemplateKey: "booking_remind", Status: "failed", LastError: "boom", BookingID: &booked, BookingStatus: "confirmed", BookingStart: future, DedupeKey: &remind, CreatedAt: waAuditWhen, ScheduledAt: waAuditWhen},
		{ID: waSweepNoBookingID, Phone: "6281200000002", TemplateKey: "test", Status: "failed", BookingStatus: "", CreatedAt: waAuditWhen, ScheduledAt: waAuditWhen},
		{ID: waSweepClosedID, Phone: "6281200000003", TemplateKey: "booking_remind", Status: "failed", BookingID: &booked, BookingStatus: "cancelled", BookingStart: future, CreatedAt: waAuditWhen, ScheduledAt: waAuditWhen},
		{ID: waSweepPastRemind, Phone: "6281200000006", TemplateKey: "booking_remind", Status: "failed", BookingID: &booked, BookingStatus: "completed", BookingStart: past, DedupeKey: &remind, CreatedAt: waAuditWhen, ScheduledAt: waAuditWhen},
		{ID: waSweepSentID, Phone: "6281200000004", TemplateKey: "booking_confirm", Status: "sent", BookingID: &booked, BookingStatus: "confirmed", BookingStart: future, CreatedAt: waAuditWhen, ScheduledAt: waAuditWhen},
		{ID: waSweepCancelledID, Phone: "6281200000005", TemplateKey: "booking_cancelled", Status: "cancelled", BookingID: &booked, BookingStatus: "cancelled", BookingStart: future, CreatedAt: waAuditWhen, ScheduledAt: waAuditWhen},
	}
}

// waQueueViews is the page shape: rows plus the handler-computed retry flag,
// evaluated at the fixture's fixed "now".
func waQueueViews() []waOutboxView {
	return waOutboxViews(waQueueSweep(), waAuditWhen)
}

func TestAdminWhatsAppCounterRendersOnlyWhenSegmented(t *testing.T) {
	s, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	render := func(view waTemplateView) string {
		t.Helper()
		return waTestRender(t, s, "id", []waTemplateView{view})
	}

	shown := render(waTemplateView{
		Key: "booking_remind", LabelKey: "a.wt.remind", Default: "Halo Bunda",
		Fields: []string{"name"}, Preview: "Halo Bunda",
		Encoding: "GSM-7", Chars: 10, Segments: 1,
	})
	if want := "GSM-7 · 10 karakter · 1 pesan"; !strings.Contains(shown, want) {
		t.Errorf("counter line missing %q", want)
	}
	if !strings.Contains(shown, "Teks tersimpan:") {
		t.Error(`counter line missing the "Teks tersimpan" label`)
	}

	hidden := render(waTemplateView{
		Key: "booking_remind", LabelKey: "a.wt.remind", Default: "Halo Bunda",
		Fields: []string{"name"}, Preview: "Halo Bunda",
		Encoding: "GSM-7", Chars: 0, Segments: 0,
	})
	if strings.Contains(hidden, "Teks tersimpan") {
		t.Error("an empty body must hide the counter line")
	}
}

// TestWaTemplateViewAudit pins the card's copy-audit metadata: an override row
// is edited (with its editor and stamp), a missing row is built-in, and a
// deleted editor keeps Edited true with an empty name (ruling R4).
func TestWaTemplateViewAudit(t *testing.T) {
	edited := waTemplateViewFor("booking_confirm", &domain.WaOverrideMeta{
		Body: "Halo {name}", UpdatedByName: "Bu Sari", UpdatedAt: waAuditWhen,
	})
	if !edited.Edited || edited.Editor != "Bu Sari" || !edited.EditedAt.Equal(waAuditWhen) {
		t.Errorf("edited card = (edited=%v editor=%q at=%v), want the stored metadata",
			edited.Edited, edited.Editor, edited.EditedAt)
	}

	builtin := waTemplateViewFor("booking_remind", nil)
	if builtin.Edited || builtin.Editor != "" || !builtin.EditedAt.IsZero() {
		t.Errorf("missing row must be built-in, got (edited=%v editor=%q at=%v)",
			builtin.Edited, builtin.Editor, builtin.EditedAt)
	}

	// R4: the editor's user row is gone (FK SET NULL), so the name is empty
	// but the row still counts as edited and keeps its date.
	orphan := waTemplateViewFor("booking_confirm", &domain.WaOverrideMeta{
		Body: "Halo {name}", UpdatedAt: waAuditWhen,
	})
	if !orphan.Edited || orphan.Editor != "" || !orphan.EditedAt.Equal(waAuditWhen) {
		t.Errorf("deleted editor must stay edited with an empty name, got (edited=%v editor=%q at=%v)",
			orphan.Edited, orphan.Editor, orphan.EditedAt)
	}
}

// TestWaTemplateViewsMarksOnlyStoredRows checks the page's map -> cards step.
func TestWaTemplateViewsMarksOnlyStoredRows(t *testing.T) {
	views := waTemplateViews(map[string]domain.WaOverrideMeta{
		"booking_remind": {Body: "Halo Bunda", UpdatedByName: "Bu Sari", UpdatedAt: waAuditWhen},
	})
	if len(views) != len(whatsapp.EditableTemplates()) {
		t.Fatalf("got %d cards, want one per editable template (%d)",
			len(views), len(whatsapp.EditableTemplates()))
	}
	for _, v := range views {
		if v.Key == "booking_remind" {
			if !v.Edited || v.Editor != "Bu Sari" {
				t.Errorf("stored row not marked edited: %+v", v)
			}
			continue
		}
		if v.Edited || v.Editor != "" {
			t.Errorf("%s has no row, must be built-in: %+v", v.Key, v)
		}
	}
}

// TestAdminWhatsAppAuditLine renders both audit branches in both languages:
// the editor's name and the WIB stamp must translate, a nameless editor falls
// back to the neutral label (R4), and no raw i18n key may leak.
func TestAdminWhatsAppAuditLine(t *testing.T) {
	s, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	stamp := "24 Nov 2026 10:04 WIB" // waAuditWhen in WIB, never the UTC hour

	edited := waTemplateViewFor("booking_remind", &domain.WaOverrideMeta{
		Body: "Halo Bunda", UpdatedByName: "Bu Sari", UpdatedAt: waAuditWhen,
	})
	anon := waTemplateViewFor("booking_remind", &domain.WaOverrideMeta{
		Body: "Halo Bunda", UpdatedAt: waAuditWhen,
	})
	builtin := waTemplateViewFor("booking_confirm", nil)

	cases := []struct {
		name string
		lang string
		view waTemplateView
		want string
	}{
		{"edited en", "en", edited, "Last edited by Bu Sari · " + stamp},
		{"edited id", "id", edited, "Terakhir diubah oleh Bu Sari · " + stamp},
		{"deleted editor en", "en", anon, "Last edited by an admin · " + stamp},
		{"deleted editor id", "id", anon, "Terakhir diubah oleh seorang admin · " + stamp},
		{"built-in en", "en", builtin, "Built-in copy"},
		{"built-in id", "id", builtin, "Teks bawaan"},
	}
	for _, tc := range cases {
		out := waTestRender(t, s, tc.lang, []waTemplateView{tc.view})
		if !strings.Contains(out, tc.want) {
			t.Errorf("%s: missing %q", tc.name, tc.want)
		}
		if strings.Contains(out, "a.wa.") {
			t.Errorf("%s: raw i18n key leaked", tc.name)
		}
	}

	// The other branch must not bleed into the same card set.
	mixed := waTestRender(t, s, "en", []waTemplateView{edited, builtin})
	if !strings.Contains(mixed, "Last edited by Bu Sari") || !strings.Contains(mixed, "Built-in copy") {
		t.Error("a mixed card set must render one line per branch")
	}
}

// newWaTestServer builds a template-only server with the given logger, the
// same skeleton the page tests use.
func newWaTestServer(t *testing.T, logger *slog.Logger) *Server {
	t.Helper()
	s, err := New(config.Config{}, logger, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	return s
}

// TestAdminWhatsAppTestSendConfirm pins the browser confirmation (ruling R5)
// and, more importantly, how it reads the field: `this.target` is the form's
// target attribute, so the handler must go through this.elements.
func TestAdminWhatsAppTestSendConfirm(t *testing.T) {
	s := newWaTestServer(t, slog.Default())
	out := waTestRender(t, s, "en", waTemplatesSweep())
	if !strings.Contains(out, "this.elements.target.value") {
		t.Error("test-send form must confirm with this.elements.target.value")
	}
	if strings.Contains(out, "this.target.value") {
		t.Error("this.target is the form target attribute, not the input named target")
	}
	if !strings.Contains(out, "confirm(") {
		t.Error("test-send form must carry an onsubmit confirm")
	}
	// The page's other confirms are localized, so this one is too.
	if !strings.Contains(out, "Send to") {
		t.Error("english confirm is missing its translated prefix")
	}
	if id := waTestRender(t, s, "id", waTemplatesSweep()); !strings.Contains(id, "Kirim ke") {
		t.Error("indonesian confirm is missing its translated prefix")
	}
}

// waLogCapture is a slog.Handler that keeps every record so a test can assert
// on exactly what reached the logs.
type waLogCapture struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *waLogCapture) Enabled(context.Context, slog.Level) bool { return true }

func (h *waLogCapture) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *waLogCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *waLogCapture) WithGroup(string) slog.Handler      { return h }

func (h *waLogCapture) all() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]slog.Record(nil), h.records...)
}

// waOutboxFake is the outbox seam behind the real WaService. Only Enqueue is
// exercised by the test-send handler; the other methods keep the interface
// satisfied without inventing behaviour no test asks for.
type waOutboxFake struct {
	err     error
	enqueue []string
}

func (f *waOutboxFake) Enqueue(_ context.Context, phone, _ string, _ map[string]any, _ *uuid.UUID, _ string, _ time.Time) (bool, error) {
	f.enqueue = append(f.enqueue, phone)
	return f.err == nil, f.err
}

func (f *waOutboxFake) UpsertScheduled(context.Context, string, string, map[string]any, *uuid.UUID, string, time.Time) (bool, error) {
	return false, nil
}

func (f *waOutboxFake) CancelPending(context.Context, uuid.UUID, ...string) (int64, error) {
	return 0, nil
}

func (f *waOutboxFake) CancelRemindersForCustomer(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}

func (f *waOutboxFake) ClaimDue(context.Context, int) ([]repo.WaOutboxItem, error) {
	return nil, nil
}

func (f *waOutboxFake) MarkSent(context.Context, uuid.UUID) error { return nil }

func (f *waOutboxFake) RetryLater(context.Context, uuid.UUID, time.Time, string) error {
	return nil
}

func (f *waOutboxFake) MarkFailed(context.Context, uuid.UUID, string) error { return nil }

func (f *waOutboxFake) RequeueSending(context.Context) (int64, error) { return 0, nil }

// TestAdminTestWhatsAppLogsMaskedTarget drives the real handler through the
// real outbox service: one info line must carry the admin id and only the
// masked target. The raw number (and the message body) must never be logged
// (ruling R5).
func TestAdminTestWhatsAppLogsMaskedTarget(t *testing.T) {
	const raw = "6281234567890"
	var logs waLogCapture
	logger := slog.New(&logs)
	s := newWaTestServer(t, logger)
	store := &waOutboxFake{}
	s.wa = service.NewWaService(store, nil, logger, false, 0, nil)

	adminID := uuid.New()
	form := url.Values{"csrf": {"token"}, "target": {raw}, "message": {"halo"}}
	req := httptest.NewRequest(http.MethodPost, "/admin/whatsapp/test", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "csrf", Value: "token"})
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserKey,
		&domain.User{ID: adminID, Role: domain.RoleAdmin}))

	rec := httptest.NewRecorder()
	s.AdminTestWhatsApp(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if len(store.enqueue) != 1 || store.enqueue[0] != raw {
		t.Fatalf("enqueued = %v, want the free-form target once", store.enqueue)
	}

	records := logs.all()
	if len(records) != 1 {
		t.Fatalf("got %d log records, want exactly one: %v", len(records), records)
	}
	if records[0].Level != slog.LevelInfo || records[0].Message != "wa test send queued" {
		t.Errorf("log = (%s, %q), want (INFO, wa test send queued)",
			records[0].Level, records[0].Message)
	}
	attrs := map[string]string{}
	records[0].Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})
	if attrs["admin"] != adminID.String() {
		t.Errorf("admin attr = %q, want %s", attrs["admin"], adminID)
	}
	if attrs["target"] != "…7890" {
		t.Errorf("target attr = %q, want the masked tail", attrs["target"])
	}
	for _, r := range records {
		var values []string
		r.Attrs(func(a slog.Attr) bool {
			values = append(values, a.Value.String())
			return true
		})
		if strings.Contains(r.Message, raw) || strings.Contains(strings.Join(values, " "), raw) {
			t.Errorf("raw target leaked into the logs: %q %v", r.Message, values)
		}
		if strings.Contains(strings.Join(values, " "), "halo") {
			t.Errorf("message body leaked into the logs: %v", values)
		}
	}
}
