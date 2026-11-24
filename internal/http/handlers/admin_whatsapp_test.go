package handlers

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"spa-booking/internal/config"
	"spa-booking/internal/domain"
	"spa-booking/internal/service"
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
	v := waTemplateViewFor("booking_confirm", "Halo {name}")
	if v.Encoding != "GSM-7" || v.Chars != 11 || v.Segments != 1 {
		t.Errorf("typed override = (%s, %d, %d), want (GSM-7, 11, 1)",
			v.Encoding, v.Chars, v.Segments)
	}
	if v.Preview != "Halo Bunda Sari" {
		t.Errorf("preview = %q, want the sample-expanded body", v.Preview)
	}

	// No override: the built-in copy is the counter's body. Every default
	// carries at least one emoji, so it is UCS-2 and never zero.
	def := waTemplateViewFor("booking_remind", "")
	if def.Encoding != "UCS-2" || def.Chars == 0 || def.Segments == 0 {
		t.Errorf("default view = (%s, %d, %d), want UCS-2 with content",
			def.Encoding, def.Chars, def.Segments)
	}
	if def.Override != "" || def.Custom {
		t.Errorf("no override expected, got %q (custom=%v)", def.Override, def.Custom)
	}
}

func TestAdminWhatsAppCounterRendersOnlyWhenSegmented(t *testing.T) {
	s, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	render := func(view waTemplateView) string {
		t.Helper()
		data := pageData{
			"Lang": "id",
			"Contact": pageData{
				"WhatsApp": "62800", "Email": "a@b.c",
				"Address": "addr", "Hours": "hours", "Maps": "https://maps.example/",
				"Instagram": "https://instagram.example/", "TikTok": "https://tiktok.example/", "Facebook": "https://facebook.example/",
			},
			"Settings":  service.WaConfig{},
			"Raw":       &domain.WaSettings{},
			"Defaults":  service.WaConfig{},
			"Conn":      "mock",
			"Templates": []waTemplateView{view},
		}
		var buf bytes.Buffer
		if err := s.tpl.ExecuteTemplate(&buf, "admin_whatsapp.html", data); err != nil {
			t.Fatalf("execute admin_whatsapp.html: %v", err)
		}
		return buf.String()
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
