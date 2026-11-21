package handlers

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"spa-booking/internal/config"
)

// portalPrintData is the minimum page data the shared footer needs.
func portalPrintData(lang string) pageData {
	return pageData{
		"Lang": lang,
		"Contact": pageData{
			"WhatsApp": "62800", "Email": "a@b.c",
			"Address": "addr", "Hours": "hours", "Maps": "https://maps.example/",
			"Instagram": "https://instagram.example/", "TikTok": "https://tiktok.example/", "Facebook": "https://facebook.example/",
		},
	}
}

// TestPortalCustomersTableBecomesCardsOnPhones pins the customers list to the
// shared table-cards pattern (modern.css: under 720px every row becomes a card).
// Without it a phone has to pan a six-column table sideways to reach the
// Book/History buttons.
func TestPortalCustomersTableBecomesCardsOnPhones(t *testing.T) {
	s, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	var buf bytes.Buffer
	if err := s.tpl.ExecuteTemplate(&buf, "portal_customers.html", portalPrintData("id")); err != nil {
		t.Fatalf("execute portal_customers.html: %v", err)
	}
	out := buf.String()
	// The class belongs on the scroll wrapper so the pattern reaches the
	// table it wraps; overflow-x stays as the fallback above 720px.
	if !strings.Contains(out, `<div style="overflow-x:auto;" class="table-cards">`) {
		t.Errorf("portal_customers.html: table wrapper must carry the table-cards class")
	}
}

// TestPortalDashboardPrintsDayAgenda checks the counter's paper affordance:
// the dashboard queue is today's agenda, so the page needs a print control
// whose label comes from the dictionary in both languages.
func TestPortalDashboardPrintsDayAgenda(t *testing.T) {
	s, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	for _, tc := range []struct{ lang, label string }{
		{"en", "Print"},
		{"id", "Cetak"},
	} {
		var buf bytes.Buffer
		if err := s.tpl.ExecuteTemplate(&buf, "portal_dashboard.html", portalPrintData(tc.lang)); err != nil {
			t.Fatalf("execute portal_dashboard.html (%s): %v", tc.lang, err)
		}
		out := buf.String()
		if !strings.Contains(out, `<button class="btn secondary on-light" type="button" onclick="window.print()">`) {
			t.Errorf("portal_dashboard.html (%s): missing print control", tc.lang)
		}
		if !strings.Contains(out, tc.label+"</button>") {
			t.Errorf("portal_dashboard.html (%s): print control label %q missing", tc.lang, tc.label)
		}
	}
}
