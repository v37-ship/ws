package handlers

import (
	"bytes"
	"log/slog"
	"testing"
	"time"

	"spa-booking/internal/config"
	"spa-booking/internal/domain"

	"github.com/google/uuid"
)

// TestTemplatesParse ensures all embedded html/template files parse with the
// production FuncMap. New only parses templates, so nil services are fine.
func TestTemplatesParse(t *testing.T) {
	_, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
}

// TestTemplatesExecute renders every full-page template with empty data.
// html/template applies contextual escaping at execution time, so parse-only
// tests miss errors like stray quotes inside attribute values.
func TestTemplatesExecute(t *testing.T) {
	s, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	pages := []string{
		"home.html", "book.html", "bookings.html", "login.html", "pay.html",
		"register.html", "services.html", "testimonial_new.html", "account.html",
		"food.html", "gallery.html",
		"admin_home.html", "admin_locations.html",
		"admin_bookings.html", "admin_testimonials.html",
		"admin_cs.html", "admin_customer_reset.html", "admin_password.html",
		"portal_dashboard.html", "portal_bookings.html", "portal_booking_new.html",
		"portal_booking_detail.html", "portal_customers.html", "portal_customer_new.html",
		"portal_customer_detail.html", "portal_customer_edit.html", "portal_schedule.html", "portal_services.html", "portal_staff_hours.html", "portal_staff_edit.html",
		"portal_reports.html", "portal_food.html", "portal_food_edit.html", "portal_gallery.html", "portal_gallery_edit.html", "portal_site.html", "portal_promos.html",
	}
	for _, name := range pages {
		data := pageData{
			"Lang": "id",
			"Contact": pageData{
				"WhatsApp": "62800", "Email": "a@b.c",
				"Address": "addr", "Hours": "hours", "Maps": "https://maps.example/",
				"Instagram": "https://instagram.example/", "TikTok": "https://tiktok.example/", "Facebook": "https://facebook.example/",
			},
		}
		switch name {
		case "home.html":
			data["HeroBanners"] = []domain.HeroBanner{{ImageURL: "https://example.com/a.jpg"}, {ImageURL: "https://example.com/b.jpg"}}
		case "account.html":
			data["User"] = &domain.User{FullName: "Test Mom", Role: domain.RoleCustomer}
		case "portal_food_edit.html":
			data["Item"] = &domain.FoodItem{Name: "Nasgor Ayam"}
		case "portal_gallery_edit.html":
			data["Item"] = &domain.GalleryItem{Title: "Baby Spa", Kind: domain.GalleryPhoto}
		case "pay.html", "portal_booking_detail.html":
			data["Booking"] = &domain.Booking{StartAt: time.Now(), EndAt: time.Now().Add(time.Hour)}
			data["Refundable"] = int64(0)
		case "portal_staff_edit.html":
			data["Staff"] = domain.Staff{}
		case "admin_cs.html":
			data["CSUsers"] = []domain.User{}
		case "portal_customer_detail.html":
			data["Bookings"] = []domain.Booking{}
		case "portal_customer_edit.html":
			data["Customer"] = domain.User{}
		case "portal_reports.html":
			data["Summary"] = domain.ReportSummary{
				StaffRows: []domain.StaffRow{{
					StaffID: uuid.New(), Name: "Bidan Ayu", Sessions: 1,
					UniqueCustomers: 1, GrossIDR: 100000, NetIDR: 100000, AvgIDR: 100000,
					Services:    []domain.StaffServiceRow{{Name: "Baby Massage", Count: 1, Total: 100000}},
					TopServices: []domain.StaffServiceRow{{Name: "Baby Massage", Count: 1, Total: 100000}},
					Lines: []domain.ReportLine{{
						CustomerName: "Mom", ServiceName: "Baby Massage", StaffName: "Bidan Ayu",
						PaymentMethod: domain.PaymentMethodCash, AmountIDR: 100000,
						PaidAt: time.Now(), Status: domain.BookingConfirmed,
					}},
					Refunds: []domain.ReportRefund{{
						Refund:       domain.Refund{AmountIDR: 50000, Reason: "cancel", CreatedBy: "CS", CreatedAt: time.Now()},
						CustomerName: "Mom", ServiceName: "Baby Massage",
					}},
				}},
			}
			data["Month"] = "2026-09"
			data["Year"] = "2026"
			data["Mon"] = "09"
			data["Years"] = []string{"2026", "2025"}
			data["ShowLoc"] = true
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
		"promo_preview.html",
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
	s, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	for _, lang := range []string{"en", "id"} {
		for _, name := range []string{
			"home.html", "services.html", "book.html", "bookings.html", "login.html",
			"pay.html", "register.html", "careers.html", "testimonial_new.html", "account.html", "food.html", "gallery.html",
			"admin_home.html", "admin_locations.html", "admin_bookings.html", "admin_testimonials.html",
			"admin_cs.html", "admin_customer_reset.html", "admin_password.html", "admin_jobs.html",
			"admin_job_applications.html", "portal_dashboard.html", "portal_bookings.html",
			"portal_booking_new.html", "portal_booking_detail.html", "portal_customers.html",
			"portal_customer_new.html", "portal_customer_detail.html", "portal_customer_edit.html", "portal_schedule.html",
			"portal_services.html", "portal_staff_hours.html", "portal_staff_edit.html",
			"portal_service_edit.html", "portal_reports.html", "portal_food.html", "portal_food_edit.html", "portal_gallery.html", "portal_gallery_edit.html", "portal_site.html", "portal_promos.html",
		} {
			data := pageData{
				"Lang": lang,
				"Contact": pageData{
					"WhatsApp": "62800", "Email": "a@b.c",
					"Address": "addr", "Hours": "hours", "Maps": "https://maps.example/",
					"Instagram": "https://instagram.example/", "TikTok": "https://tiktok.example/", "Facebook": "https://facebook.example/",
				},
			}
			switch name {
			case "home.html":
				data["HeroBanners"] = []domain.HeroBanner{{ImageURL: "https://example.com/a.jpg"}, {ImageURL: "https://example.com/b.jpg"}}
			case "account.html":
				data["User"] = &domain.User{FullName: "Test Mom", Role: domain.RoleCustomer}
			case "portal_food_edit.html":
				data["Item"] = &domain.FoodItem{Name: "Nasgor Ayam"}
			case "portal_gallery_edit.html":
				data["Item"] = &domain.GalleryItem{Title: "Baby Spa", Kind: domain.GalleryPhoto}
			case "pay.html", "portal_booking_detail.html":
				data["Booking"] = &domain.Booking{StartAt: time.Now(), EndAt: time.Now().Add(time.Hour)}
				data["Refundable"] = int64(0)
			case "portal_staff_edit.html":
				data["Staff"] = domain.Staff{}
			case "admin_cs.html":
				data["CSUsers"] = []domain.User{}
			case "portal_customer_detail.html":
				data["Bookings"] = []domain.Booking{}
			case "portal_customer_edit.html":
				data["Customer"] = domain.User{}
			case "portal_dashboard.html":
				data["HasDrawer"] = true
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
			if lang == "id" && name == "home.html" && !contains(out, "Perawatan diri untuk wanita") {
				t.Errorf("home.html (id): missing Indonesian hero title")
			}
			if lang == "en" && name == "home.html" && !contains(out, "Self-care for women") {
				t.Errorf("home.html (en): missing English hero title")
			}
			if (name == "home.html" || name == "login.html") && contains(out, "theme-btn nav-toggle") {
				t.Errorf("%s (%s): public page must not have drawer hamburger", name, lang)
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
				if !contains(out, "drawer-backdrop") || !contains(out, "__wanggaDrawerToggle") {
					t.Errorf("portal_dashboard.html (%s): missing mobile drawer markup", lang)
				}
				if !contains(out, "theme-btn nav-toggle") {
					t.Errorf("portal_dashboard.html (%s): missing header hamburger", lang)
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

// TestNoEmojiIcons guards the icon policy (see icons.go): UI chrome uses
// inline SVG via {{icon}}, never color emoji. It renders every page and
// partial in both languages and fails on pictograph codepoints.
//
// Exempt by design: text glyphs (stars, checkmarks, hamburger, close),
// the JS theme map in head.html, and WhatsApp message content (the waURL
// href carries the slip text, which renders in WhatsApp, not in our UI).
func TestNoEmojiIcons(t *testing.T) {
	forbidden := []string{
		"🏠", "👶", "👩", "💬", "📍", "📞", "💆", "🏥", "📝", "📊", "📅", "➕",
		"❌", "✅", "⚙", "✨", "🏢", "💼", "💵", "📱", "📈", "🖨", "🔑", "👥",
		"🤰", "🍽", "🖼", "🎟", "👑", "⚠", "🆕", "✉", "📸", "🕒", "💡", "⬇",
	}
	pages := []string{
		"home.html", "services.html", "book.html", "bookings.html", "login.html",
		"pay.html", "register.html", "careers.html", "testimonial_new.html", "account.html", "food.html", "gallery.html",
		"admin_home.html", "admin_locations.html", "admin_bookings.html", "admin_testimonials.html",
		"admin_cs.html", "admin_customer_reset.html", "admin_password.html", "admin_jobs.html",
		"admin_job_applications.html", "portal_dashboard.html", "portal_bookings.html",
		"portal_booking_new.html", "portal_booking_detail.html", "portal_customers.html",
		"portal_customer_new.html", "portal_customer_detail.html", "portal_customer_edit.html", "portal_schedule.html",
		"portal_services.html", "portal_staff_hours.html", "portal_staff_edit.html",
		"portal_service_edit.html", "portal_reports.html", "portal_food.html", "portal_food_edit.html", "portal_gallery.html", "portal_gallery_edit.html", "portal_site.html", "portal_promos.html",
	}
	partials := []string{
		"services_partial.html", "staff_partial.html", "slots_partial.html",
		"pay_status.html", "toast.html", "service_cards.html", "staff_cards.html",
		"slot_groups.html", "portal_queue_rows.html", "portal_booking_rows.html",
		"customer_search_rows.html", "promo_preview.html",
	}
	s, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	check := func(name, lang, out string) {
		t.Helper()
		// Strip inline scripts (JS theme map) and link targets (WhatsApp
		// slip text) before scanning visible chrome.
		out = stripScripts(out)
		out = stripHrefs(out)
		for _, e := range forbidden {
			if contains(out, e) {
				t.Errorf("%s (%s): UI chrome must not use emoji %q", name, lang, e)
			}
		}
	}
	for _, lang := range []string{"en", "id"} {
		for _, name := range pages {
			data := pageData{
				"Lang": lang,
				"Contact": pageData{
					"WhatsApp": "62800", "Email": "a@b.c",
					"Address": "addr", "Hours": "hours", "Maps": "https://maps.example/",
					"Instagram": "https://instagram.example/", "TikTok": "https://tiktok.example/", "Facebook": "https://facebook.example/",
				},
			}
			switch name {
			case "home.html":
				data["HeroBanners"] = []domain.HeroBanner{{ImageURL: "https://example.com/a.jpg"}, {ImageURL: "https://example.com/b.jpg"}}
			case "account.html":
				data["User"] = &domain.User{FullName: "Test Mom", Role: domain.RoleCustomer}
			case "portal_food_edit.html":
				data["Item"] = &domain.FoodItem{Name: "Nasgor Ayam"}
			case "portal_gallery_edit.html":
				data["Item"] = &domain.GalleryItem{Title: "Baby Spa", Kind: domain.GalleryPhoto}
			case "pay.html", "portal_booking_detail.html":
				data["Booking"] = &domain.Booking{StartAt: time.Now(), EndAt: time.Now().Add(time.Hour)}
				data["Refundable"] = int64(0)
			case "portal_staff_edit.html":
				data["Staff"] = domain.Staff{}
			case "admin_cs.html":
				data["CSUsers"] = []domain.User{}
			case "portal_customer_detail.html":
				data["Bookings"] = []domain.Booking{}
			case "portal_customer_edit.html":
				data["Customer"] = domain.User{}
			case "portal_dashboard.html":
				data["HasDrawer"] = true
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
			check(name, lang, buf.String())
		}
		for _, name := range partials {
			var buf bytes.Buffer
			if err := s.tpl.ExecuteTemplate(&buf, name, pageData{"Lang": lang}); err != nil {
				t.Errorf("execute partial %s (%s): %v", name, lang, err)
				continue
			}
			check(name, lang, buf.String())
		}
	}
}

// stripScripts removes <script>...</script> blocks (inline JS owns its own
// glyphs, e.g. the theme toggle map).
func stripScripts(s string) string {
	for {
		start := bytes.Index([]byte(s), []byte("<script"))
		if start < 0 {
			return s
		}
		rest := s[start:]
		end := bytes.Index([]byte(rest), []byte("</script>"))
		if end < 0 {
			return s[:start]
		}
		s = s[:start] + rest[end+len("</script>"):]
	}
}

// stripHrefs removes href="..." values (link targets carry message content,
// e.g. the WhatsApp slip text, which renders outside our UI).
func stripHrefs(s string) string {
	for {
		idx := bytes.Index([]byte(s), []byte(`href="`))
		if idx < 0 {
			return s
		}
		rest := s[idx+len(`href="`):]
		end := bytes.Index([]byte(rest), []byte(`"`))
		if end < 0 {
			return s[:idx]
		}
		s = s[:idx] + rest[end+1:]
	}
}
