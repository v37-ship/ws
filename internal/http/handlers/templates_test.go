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
	_, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
}

// TestTemplatesExecute renders every full-page template with empty data.
// html/template applies contextual escaping at execution time, so parse-only
// tests miss errors like stray quotes inside attribute values.
func TestTemplatesExecute(t *testing.T) {
	s, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	pages := []string{
		"home.html", "book.html", "bookings.html", "login.html", "pay.html",
		"register.html", "services.html",
		"admin_home.html", "admin_locations.html",
		"admin_bookings.html",
		"admin_cs.html", "admin_customer_reset.html", "admin_password.html",
		"portal_dashboard.html", "portal_bookings.html", "portal_booking_new.html",
		"portal_booking_detail.html", "portal_customers.html", "portal_customer_new.html",
		"portal_customer_detail.html", "portal_schedule.html", "portal_services.html", "portal_staff_hours.html", "portal_staff_edit.html",
		"portal_reports.html",
	}
	for _, name := range pages {
		data := pageData{
			"Lang": "id",
			"Contact": pageData{
				"WhatsApp": "62800", "Email": "a@b.c",
				"Address": "addr", "Hours": "hours", "Maps": "https://maps.example/",
			},
		}
		switch name {
		case "pay.html", "portal_booking_detail.html":
			data["Booking"] = &domain.Booking{StartAt: time.Now(), EndAt: time.Now().Add(time.Hour)}
			data["Refundable"] = int64(0)
		case "portal_staff_edit.html":
			data["Staff"] = domain.Staff{}
		case "admin_cs.html":
			data["CSUsers"] = []domain.User{}
		case "portal_customer_detail.html":
			data["Bookings"] = []domain.Booking{}
		case "portal_reports.html":
			data["Summary"] = domain.ReportSummary{}
			data["Month"] = "2026-09"
			data["Year"] = "2026"
			data["Mon"] = "09"
			data["Years"] = []string{"2026", "2025"}
			data["Lines"] = []domain.ReportLine{}
			data["Refunds"] = []domain.ReportRefund{}
		}
		var buf bytes.Buffer
		if err := s.tpl.ExecuteTemplate(&buf, name, data); err != nil {
			t.Errorf("execute %s: %v", name, err)
		}
		// The customer picker input must send ?q= (what PortalCustomerSearch
		// reads) — a mismatched name fails silently with empty results.
		if name == "portal_booking_new.html" {
			out := buf.String()
			if !contains(out, `hx-get="/portal/customers/search"`) {
				t.Errorf("portal_booking_new.html: missing customer search hx-get")
			}
			if !contains(out, `name="q"`) {
				t.Errorf("portal_booking_new.html: search input must be named q")
			}
		}
	}
	partials := []string{
		"services_partial.html", "staff_partial.html", "slots_partial.html",
		"pay_status.html", "toast.html", "service_cards.html", "staff_cards.html",
		"slot_groups.html", "portal_queue_rows.html", "portal_booking_rows.html",
	}
	for _, name := range partials {
		var buf bytes.Buffer
		if err := s.tpl.ExecuteTemplate(&buf, name, pageData{"Lang": "id"}); err != nil {
			t.Errorf("execute %s: %v", name, err)
		}
	}
}

// TestTemplatesBothLanguages renders key public pages in EN + ID and fails
// on missing-key leaks (t returns the key itself when absent).
func TestTemplatesBothLanguages(t *testing.T) {
	s, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	for _, lang := range []string{"en", "id"} {
		for _, name := range []string{
			"home.html", "services.html", "book.html", "bookings.html", "login.html",
			"pay.html", "register.html", "careers.html",
			"admin_home.html", "admin_locations.html", "admin_bookings.html",
			"admin_cs.html", "admin_customer_reset.html", "admin_password.html", "admin_jobs.html",
			"admin_job_applications.html", "portal_dashboard.html", "portal_bookings.html",
			"portal_booking_new.html", "portal_booking_detail.html", "portal_customers.html",
			"portal_customer_new.html", "portal_customer_detail.html", "portal_schedule.html",
			"portal_services.html", "portal_staff_hours.html", "portal_staff_edit.html",
			"portal_service_edit.html", "portal_reports.html",
		} {
			data := pageData{
				"Lang": lang,
				"Contact": pageData{
					"WhatsApp": "62800", "Email": "a@b.c",
					"Address": "addr", "Hours": "hours", "Maps": "https://maps.example/",
				},
			}
			switch name {
			case "pay.html", "portal_booking_detail.html":
				data["Booking"] = &domain.Booking{StartAt: time.Now(), EndAt: time.Now().Add(time.Hour)}
				data["Refundable"] = int64(0)
			case "portal_staff_edit.html":
				data["Staff"] = domain.Staff{}
			case "admin_cs.html":
				data["CSUsers"] = []domain.User{}
			case "portal_customer_detail.html":
				data["Bookings"] = []domain.Booking{}
			case "portal_service_edit.html":
				data["Service"] = domain.Service{}
			case "admin_job_applications.html":
				data["Job"] = domain.Job{}
			case "portal_reports.html":
				data["Summary"] = domain.ReportSummary{}
				data["Month"] = "2026-09"
				data["Year"] = "2026"
				data["Mon"] = "09"
				data["Years"] = []string{"2026", "2025"}
				data["Lines"] = []domain.ReportLine{}
				data["Refunds"] = []domain.ReportRefund{}
			}
			var buf bytes.Buffer
			if err := s.tpl.ExecuteTemplate(&buf, name, data); err != nil {
				t.Errorf("execute %s (%s): %v", name, lang, err)
				continue
			}
			out := buf.String()
			if !contains(out, "lang=\""+lang+"\"") {
				t.Errorf("%s (%s): missing html lang=%s", name, lang, lang)
			}
			// Spot-check translated chrome per language.
			if lang == "id" && name == "home.html" && !contains(out, "Awal lembut") {
				t.Errorf("home.html (id): missing Indonesian hero title")
			}
			if lang == "en" && name == "home.html" && !contains(out, "Gentle beginnings") {
				t.Errorf("home.html (en): missing English hero title")
			}
			if lang == "id" && name == "portal_bookings.html" && !contains(out, "Manajemen Booking") {
				t.Errorf("portal_bookings.html (id): missing Indonesian title")
			}
			if lang == "id" && name == "login.html" && !contains(out, "Selamat datang kembali") {
				t.Errorf("login.html (id): missing Indonesian heading")
			}
			if lang == "id" && name == "admin_cs.html" && !contains(out, "Manajemen Customer Service") {
				t.Errorf("admin_cs.html (id): missing Indonesian title")
			}
			if lang == "en" && name == "admin_cs.html" && !contains(out, "Customer Service (CS) Management") {
				t.Errorf("admin_cs.html (en): missing English title")
			}
			if lang == "id" && name == "portal_reports.html" && !contains(out, "Laporan pendapatan") {
				t.Errorf("portal_reports.html (id): missing Indonesian title")
			}
			if lang == "en" && name == "portal_reports.html" && !contains(out, "Revenue report") {
				t.Errorf("portal_reports.html (en): missing English title")
			}
			if name == "portal_dashboard.html" {
				if !contains(out, "portal-side") || !contains(out, "side-toggle") {
					t.Errorf("portal_dashboard.html (%s): missing sidebar shell/toggle", lang)
				}
				want := map[string]string{"en": "Operations", "id": "Operasional"}[lang]
				if !contains(out, want) {
					t.Errorf("portal_dashboard.html (%s): missing sidebar group %q", lang, want)
				}
			}
		}
		// HTMX partials must also render in both languages without missing-key leaks
		// (t echoes the key itself when absent, so a literal key in output = leak).
		for _, lang := range []string{"en", "id"} {
			for _, name := range []string{
				"services_partial.html", "staff_partial.html", "slots_partial.html",
				"pay_status.html", "toast.html", "service_cards.html", "staff_cards.html",
				"slot_groups.html", "portal_queue_rows.html", "portal_booking_rows.html",
				"customer_search_rows.html",
			} {
				var buf bytes.Buffer
				if err := s.tpl.ExecuteTemplate(&buf, name, pageData{"Lang": lang}); err != nil {
					t.Errorf("execute partial %s (%s): %v", name, lang, err)
					continue
				}
				out := buf.String()
				for _, key := range []string{
					"slots.title", "slots.hint", "svc2.service", "pay.wait", "pay.status",
					"book.legend", "book.no_slots", "p.q.mt", "p.br.mt",
				} {
					if contains(out, `"`+key+`"`) || contains(out, ">"+key+"<") {
						t.Errorf("partial %s (%s): missing translation for key %s", name, lang, key)
					}
				}
			}
		}
	}
}

func contains(s, sub string) bool {
	return bytes.Contains([]byte(s), []byte(sub))
}
