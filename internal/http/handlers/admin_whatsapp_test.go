package handlers

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"spa-booking/internal/config"
	"spa-booking/internal/domain"
	"spa-booking/internal/service"
	"spa-booking/internal/whatsapp"
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
	if !strings.Contains(shown, "Teks ini:") {
		t.Error(`counter line missing the "Teks ini" label`)
	}

	hidden := render(waTemplateView{
		Key: "booking_remind", LabelKey: "a.wt.remind", Default: "Halo Bunda",
		Fields: []string{"name"}, Preview: "Halo Bunda",
		Encoding: "GSM-7", Chars: 0, Segments: 0,
	})
	if strings.Contains(hidden, "Teks ini") {
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
