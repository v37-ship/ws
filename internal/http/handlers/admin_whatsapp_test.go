package handlers

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
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
