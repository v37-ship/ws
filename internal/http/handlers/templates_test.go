package handlers

import (
	"bytes"
	"log/slog"
	"testing"
	"time"

	"spa-booking/internal/config"
	"spa-booking/internal/domain"
)

// TestTemplatesParse ensures all embedded html/template files parse with the
// production FuncMap. New only parses templates, so nil services are fine.
func TestTemplatesParse(t *testing.T) {
	_, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
}

// TestTemplatesExecute renders every full-page template with empty data.
// html/template applies contextual escaping at execution time, so parse-only
// tests miss errors like stray quotes inside attribute values.
func TestTemplatesExecute(t *testing.T) {
	s, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	pages := []string{
		"home.html", "book.html", "bookings.html", "login.html", "pay.html",
		"register.html", "services.html",
		"admin_home.html", "admin_locations.html",
		"admin_staff.html", "admin_staff_hours.html", "admin_bookings.html",
		"admin_cs.html", "admin_customer_reset.html",
		"portal_dashboard.html", "portal_bookings.html", "portal_booking_new.html",
		"portal_booking_detail.html", "portal_customers.html", "portal_customer_new.html",
		"portal_customer_detail.html", "portal_schedule.html", "portal_services.html",
	}
	for _, name := range pages {
		data := pageData{
			"Contact": pageData{
				"WhatsApp": "62800", "Email": "a@b.c",
				"Address": "addr", "Hours": "hours", "Maps": "https://maps.example/",
			},
		}
		switch name {
		case "pay.html", "portal_booking_detail.html":
			data["Booking"] = &domain.Booking{StartAt: time.Now(), EndAt: time.Now().Add(time.Hour)}
		case "admin_cs.html":
			data["CSUsers"] = []domain.User{}
		case "portal_customer_detail.html":
			data["Bookings"] = []domain.Booking{}
		}
		var buf bytes.Buffer
		if err := s.tpl.ExecuteTemplate(&buf, name, data); err != nil {
			t.Errorf("execute %s: %v", name, err)
		}
	}
	partials := []string{
		"services_partial.html", "staff_partial.html", "slots_partial.html",
		"pay_status.html", "toast.html", "service_cards.html", "staff_cards.html",
		"slot_groups.html", "portal_queue_rows.html", "portal_booking_rows.html",
	}
	for _, name := range partials {
		var buf bytes.Buffer
		if err := s.tpl.ExecuteTemplate(&buf, name, pageData{}); err != nil {
			t.Errorf("execute %s: %v", name, err)
		}
	}
}
