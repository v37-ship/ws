package handlers

import (
	"bytes"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"testing"
	"time"

	"spa-booking/internal/config"
	"spa-booking/internal/domain"
	"spa-booking/internal/i18n"
	"spa-booking/internal/service"

	"github.com/google/uuid"
)

// seoTestCardAlt mirrors the og:image:alt copy rendered by head.html. Meta copy
// is not UI, so it stays English in both languages.
const seoTestCardAlt = "Wangga Spa — Baby, Mom, & Woman: self-care for women, loving care for mom & baby. Jakarta spa and homecare."

// tvSweepTime is the fixed creation stamp the admin TV sweep renders.
var tvSweepTime = time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)

// portalTVSweepData feeds the portal pairing page every branch of its device
// list: the admin-only revoke control, a timed row, and the never-seen
// fallback. The secret hash is deliberately absent — the page never renders it.
func portalTVSweepData() pageData {
	locID := uuid.MustParse("55555555-5555-5555-5555-555555555555")
	return pageData{
		"User":      &domain.User{ID: uuid.New(), Role: domain.RoleAdmin, FullName: "Admin"},
		"Devices":   []domain.TVDevice{{ID: uuid.New(), Label: "Lobby TV", LocationID: locID, PairedAt: &tvSweepTime}},
		"Locations": []domain.Location{{ID: locID, Name: "Kayu Putih", Active: true}},
		"LocNames":  map[string]string{locID.String(): "Kayu Putih"},
	}
}

// TestTemplatesParse ensures all embedded html/template files parse with the
// production FuncMap. New only parses templates, so nil services are fine.
func TestTemplatesParse(t *testing.T) {
	_, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
}

// The admin bookings table is retired (its URL forwards to /portal/bookings),
// so the template must leave the embedded set rather than linger unreachable.
func TestLegacyAdminBookingsTemplateRetired(t *testing.T) {
	s, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	if s.tpl.Lookup("admin_bookings.html") != nil {
		t.Error("admin_bookings.html is still embedded; /admin/bookings redirects to /portal/bookings")
	}
}

// TestTemplatesExecute renders every full-page template with empty data.
// html/template applies contextual escaping at execution time, so parse-only
// tests miss errors like stray quotes inside attribute values.
func TestTemplatesExecute(t *testing.T) {
	s, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	pages := []string{
		"home.html", "book.html", "bookings.html", "login.html", "pay.html",
		"register.html", "services.html", "testimonial_new.html", "account.html",
		"food.html", "gallery.html",
		"admin_home.html", "admin_payments.html", "admin_locations.html",
		"admin_testimonials.html", "admin_whatsapp.html", "admin_tv.html", "admin_payroll.html", "admin_payroll_rates.html",
		"tv_invalid.html", "tv_view.html", "tv_pairing.html",
		"admin_cs.html", "admin_customer_reset.html", "admin_password.html",
		"portal_dashboard.html", "portal_bookings.html", "portal_booking_new.html",
		"portal_booking_detail.html", "portal_customers.html", "portal_customer_new.html",
		"portal_customer_detail.html", "portal_customer_edit.html", "portal_schedule.html", "portal_services.html", "portal_staff_hours.html", "portal_staff_edit.html",
		"portal_reports.html", "portal_food.html", "portal_food_edit.html", "portal_gallery.html", "portal_gallery_edit.html", "portal_site.html", "portal_promos.html", "portal_distance.html",
		"portal_tv.html",
		"portal_error.html",
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
		case "tv_view.html":
			for k, v := range tvSweepData("id") {
				data[k] = v
			}
		case "tv_pairing.html":
			data["Code"] = "482913"
		case "portal_tv.html":
			for k, v := range portalTVSweepData() {
				data[k] = v
			}
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
		case "admin_whatsapp.html":
			data["Settings"] = service.WaConfig{}
			data["Raw"] = &domain.WaSettings{}
			data["Defaults"] = service.WaConfig{}
			data["Conn"] = "mock"
			data["Templates"] = []waTemplateView{{
				Key: "booking_remind", LabelKey: "a.wt.remind", Default: "Halo Bunda",
				Fields: []string{"name"}, Preview: "Halo Bunda",
			}}
		case "admin_payroll.html":
			for k, v := range payrollSweepData("id") {
				data[k] = v
			}
		case "admin_payroll_rates.html":
			for k, v := range payrollRatesSweepData("id") {
				data[k] = v
			}
		case "portal_customer_detail.html":
			data["Bookings"] = []domain.Booking{}
		case "portal_customer_edit.html":
			data["Customer"] = domain.User{}
		case "portal_error.html":
			data["ErrorCode"] = 403
			data["ErrorMessage"] = "Only an admin can do that."
		case "portal_reports.html":
			// Staff tab: the widest panel, so the sweep also executes the
			// by-staff table and both detail modals. report_tabs_test renders
			// every tab in both languages.
			for k, v := range reportsSweepData("staff") {
				data[k] = v
			}
		}
		var buf bytes.Buffer
		if err := s.tpl.ExecuteTemplate(&buf, name, data); err != nil {
			t.Errorf("execute %s: %v", name, err)
		}
		// Styles are self-hosted: the Tailwind browser CDN was a utility no-op
		// (only .grid, which app.css already defines) and its preflight reset is
		// now ported into app.css. A re-added <script> would silently bring the
		// whole preflight back and double-apply the reset.
		if out := strings.ToLower(buf.String()); strings.Contains(out, "cdn.jsdelivr.net") || strings.Contains(out, "tailwind") {
			t.Errorf("%s still references the Tailwind browser CDN", name)
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
		"promo_preview.html", "tv_queue_rows.html", "tv_queue_panel.html", "tv_pair_state.html",
		"tv_slides.html", "tv_slide_services.html", "tv_slide_testimonials.html",
		"tv_slide_food.html", "tv_slide_review.html",
	}
	for _, name := range partials {
		data := pageData{"Lang": "id"}
		if strings.HasPrefix(name, "tv_") {
			data = tvSweepData("id")
		}
		var buf bytes.Buffer
		if err := s.tpl.ExecuteTemplate(&buf, name, data); err != nil {
			t.Errorf("execute %s: %v", name, err)
		}
	}
}

// TestTemplatesBothLanguages renders key public pages in EN + ID and fails
// on missing-key leaks (t returns the key itself when absent).
func TestTemplatesBothLanguages(t *testing.T) {
	s, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	for _, lang := range []string{"en", "id"} {
		for _, name := range []string{
			"home.html", "services.html", "book.html", "bookings.html", "login.html",
			"pay.html", "register.html", "careers.html", "testimonial_new.html", "account.html", "food.html", "gallery.html",
			"admin_home.html", "admin_payments.html", "admin_locations.html", "admin_testimonials.html", "admin_whatsapp.html",
			"admin_tv.html", "admin_payroll.html", "admin_payroll_rates.html", "tv_invalid.html", "tv_view.html", "tv_pairing.html",
			"admin_cs.html", "admin_customer_reset.html", "admin_password.html", "admin_jobs.html",
			"admin_job_applications.html", "portal_dashboard.html", "portal_bookings.html",
			"portal_booking_new.html", "portal_booking_detail.html", "portal_customers.html",
			"portal_customer_new.html", "portal_customer_detail.html", "portal_customer_edit.html", "portal_schedule.html",
			"portal_services.html", "portal_staff_hours.html", "portal_staff_edit.html",
			"portal_service_edit.html", "portal_reports.html", "portal_food.html", "portal_food_edit.html", "portal_gallery.html", "portal_gallery_edit.html", "portal_site.html", "portal_promos.html", "portal_distance.html", "portal_tv.html", "portal_error.html",
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
				// The head meta + JSON-LD the Home handler wires for the page.
				data["BaseURL"] = seoTestBaseURL
				data["ImagePath"] = seoImagePath
				data["SEO"] = pageData{
					"Title":       "Wangga Spa Baby, Mom, & Woman",
					"Description": i18n.T(lang, "seo.desc"),
					"Path":        "/",
				}
				data["JSONLD"] = jsonldHTML(
					siteStructuredData(seoTestBaseURL, "Setiap Hari 11:00–21:00 WIB", seoTestWhatsApp, sdLocations(), nil),
					faqStructuredData(lang),
				)
			case "services.html":
				data["BaseURL"] = seoTestBaseURL
				data["ImagePath"] = seoImagePath
				data["SEO"] = pageData{
					"Title":       "Services — Wangga Spa Baby, Mom, & Woman",
					"Description": i18n.T(lang, "seo.desc"),
					"Path":        "/services",
				}
				data["JSONLD"] = jsonldHTML(
					siteStructuredData(seoTestBaseURL, "Setiap Hari 11:00–21:00 WIB", seoTestWhatsApp, sdLocations(), nil),
					servicesStructuredData(seoTestBaseURL, sdGroups()),
				)
			case "tv_view.html":
				for k, v := range tvSweepData(lang) {
					data[k] = v
				}
			case "tv_pairing.html":
				data["Code"] = "482913"
			case "portal_tv.html":
				for k, v := range portalTVSweepData() {
					data[k] = v
				}
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
			case "admin_whatsapp.html":
				data["Settings"] = service.WaConfig{}
				data["Raw"] = &domain.WaSettings{}
				data["Defaults"] = service.WaConfig{}
				data["Conn"] = "mock"
				data["Templates"] = []waTemplateView{{
					Key: "booking_remind", LabelKey: "a.wt.remind", Default: "Halo Bunda",
					Fields: []string{"name"}, Preview: "Halo Bunda",
				}}
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
				for k, v := range reportsSweepData("revenue") {
					data[k] = v
				}
			case "admin_payroll.html":
				for k, v := range payrollSweepData(lang) {
					data[k] = v
				}
			case "admin_payroll_rates.html":
				for k, v := range payrollRatesSweepData(lang) {
					data[k] = v
				}
			case "admin_tv.html":
				// One live token so the sweep covers the URL block, the branch
				// name, the revoke form and the create form, not just the empty
				// state.
				locID := uuid.New()
				data["BaseURL"] = seoTestBaseURL
				data["Tokens"] = []domain.TVToken{{
					ID: uuid.New(), Token: "tok", Label: "Lobby TV",
					LocationID: locID, CreatedAt: &tvSweepTime,
				}}
				data["Locations"] = []domain.Location{{ID: locID, Name: "Kayu Putih", Active: true}}
				data["LocNames"] = map[string]string{locID.String(): "Kayu Putih"}
				data["Devices"] = []domain.TVDevice{{
					ID: uuid.New(), Label: "Lobby TV", LocationID: locID, PairedAt: &tvSweepTime,
				}}
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
			// This sweep also reaches pages TestTemplatesExecute does not render
			// (careers, jobs, service edit): none may carry the removed CDN tag.
			if low := strings.ToLower(out); strings.Contains(low, "cdn.jsdelivr.net") || strings.Contains(low, "tailwind") {
				t.Errorf("%s (%s) still references the Tailwind browser CDN", name, lang)
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
			if name == "home.html" {
				// One heading per subject: the homecare section-head carries
				// hc.title/hc.sub, so the panel below must not repeat them
				// (only the pills and the booking CTA stay in the panel).
				hcTitle, hcBook, momPill := "Homecare visits", "Book homecare", "Mom care"
				if lang == "id" {
					hcTitle, hcBook, momPill = "Homecare ke rumah", "Booking homecare", "Perawatan Bunda"
				}
				if got := strings.Count(out, hcTitle); got != 1 {
					t.Errorf("home.html (%s): homecare heading appears %d times, want 1", lang, got)
				}
				if !contains(out, hcBook) {
					t.Errorf("home.html (%s): homecare booking CTA lost", lang)
				}
				// No Locations data in this render, so the location loop emits
				// nothing and the mom pill appears exactly once.
				if got := strings.Count(out, `<span class="pill mint">`+momPill+`</span>`); got != 1 {
					t.Errorf("home.html (%s): homecare mom pill appears %d times, want 1", lang, got)
				}
			}
			if name == "home.html" || name == "services.html" {
				// Head meta + structured data: canonical URL, description, and
				// the Open Graph preview all come from the SEO pageData block.
				wantCanonical := seoTestBaseURL + "/"
				if name == "services.html" {
					wantCanonical = seoTestBaseURL + "/services"
				}
				if !contains(out, `<script type="application/ld+json">`) {
					t.Errorf("%s (%s): missing JSON-LD block", name, lang)
				}
				if !contains(out, `rel="canonical" href="`+wantCanonical+`"`) {
					t.Errorf("%s (%s): missing canonical %s", name, lang, wantCanonical)
				}
				if !contains(out, `<meta name="description" content="`+i18n.T(lang, "seo.desc")+`"`) {
					t.Errorf("%s (%s): meta description must carry seo.desc", name, lang)
				}
				if !contains(out, `<meta property="og:title"`) {
					t.Errorf("%s (%s): missing og:title", name, lang)
				}
				if !contains(out, `<meta property="og:description" content="`+i18n.T(lang, "seo.desc")+`"`) {
					t.Errorf("%s (%s): missing og:description", name, lang)
				}
				if !contains(out, `<meta property="og:url" content="`+wantCanonical+`"`) {
					t.Errorf("%s (%s): missing og:url %s", name, lang, wantCanonical)
				}
				// One card, two consumers: the og:image tags describe it in full
				// (dimensions + alt), and the JSON-LD entity must name the very
				// same URL so a crawler and a chat preview never disagree.
				wantCard := seoTestBaseURL + "/static/img/share-card.jpg"
				if !contains(out, `<meta property="og:image" content="`+wantCard+`"`) {
					t.Errorf("%s (%s): og:image must be the branded share card %s", name, lang, wantCard)
				}
				if !contains(out, `<meta property="og:image:width" content="1200"`) {
					t.Errorf("%s (%s): missing og:image:width 1200", name, lang)
				}
				if !contains(out, `<meta property="og:image:height" content="630"`) {
					t.Errorf("%s (%s): missing og:image:height 630", name, lang)
				}
				if !contains(out, `<meta property="og:image:alt" content="`+html.EscapeString(seoTestCardAlt)+`"`) {
					t.Errorf("%s (%s): missing og:image:alt", name, lang)
				}
				if !contains(out, `"image":"`+wantCard+`"`) {
					t.Errorf("%s (%s): JSON-LD image must be the og:image URL %s", name, lang, wantCard)
				}
			}
			if name == "home.html" {
				// The FAQ is visible copy first and machine data second: every
				// question and answer must render as text outside any script.
				body := stripScripts(out)
				if !contains(body, "<h2>"+i18n.T(lang, "faq.title")+"</h2>") {
					t.Errorf("home.html (%s): FAQ heading missing from the body", lang)
				}
				for n := 1; n <= 5; n++ {
					q := i18n.T(lang, fmt.Sprintf("faq.q%d", n))
					a := i18n.T(lang, fmt.Sprintf("faq.a%d", n))
					if !contains(body, q) {
						t.Errorf("home.html (%s): faq.q%d not visible outside scripts", lang, n)
					}
					if !contains(body, a) {
						t.Errorf("home.html (%s): faq.a%d not visible outside scripts", lang, n)
					}
					if !contains(out, `"name":"`+q+`"`) {
						t.Errorf("home.html (%s): faq.q%d missing from JSON-LD", lang, n)
					}
					if !contains(out, `"text":"`+a+`"`) {
						t.Errorf("home.html (%s): faq.a%d missing from JSON-LD", lang, n)
					}
				}
				if got := strings.Count(out, `"@type":"Question"`); got != 5 {
					t.Errorf("home.html (%s): JSON-LD has %d questions, want 5", lang, got)
				}
				if got := strings.Count(body, "<details"); got != 5 {
					t.Errorf("home.html (%s): %d FAQ <details> elements, want 5", lang, got)
				}
				// Search and AI crawlers must not receive a description that
				// echoes a dictionary key.
				if contains(out, `<meta name="description" content="seo.desc"`) {
					t.Errorf("home.html (%s): meta description is a missing key", lang)
				}
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
			if name == "admin_tv.html" {
				want := map[string]string{"en": "Waiting-room TV", "id": "TV Ruang Tunggu"}[lang]
				if !contains(out, want) {
					t.Errorf("admin_tv.html (%s): missing %q", lang, want)
				}
				// The displayed link must be the full kiosk URL, built from
				// BaseURL, or nobody can type it into the TV.
				if !contains(out, seoTestBaseURL+"/tv?t=tok") {
					t.Errorf("admin_tv.html (%s): missing the full kiosk URL", lang)
				}
				// The paired-device section shares the portal copy, so a
				// missing key would leak here too.
				if !contains(out, "/admin/tv/device/") {
					t.Errorf("admin_tv.html (%s): missing the device revoke action", lang)
				}
				for _, key := range []string{"tv.pr.list", "tv.pr.seen", "tv.pr.never", "tv.pr.rev"} {
					if contains(out, ">"+key+"<") {
						t.Errorf("admin_tv.html (%s): missing translation for %s", lang, key)
					}
				}
			}
			if name == "tv_pairing.html" {
				want := map[string]string{"en": "Pair this screen", "id": "Hubungkan layar ini"}[lang]
				if !contains(out, want) {
					t.Errorf("tv_pairing.html (%s): missing the pairing heading %q", lang, want)
				}
				if !contains(out, "482913") {
					t.Errorf("tv_pairing.html (%s): the code is not rendered", lang)
				}
				if !contains(out, `hx-get="/tv/pair-state"`) {
					t.Errorf("tv_pairing.html (%s): missing the state poll", lang)
				}
				for _, key := range []string{"tv.pr.title", "tv.pr.hint", "tv.pr.wait"} {
					if contains(out, ">"+key+"<") {
						t.Errorf("tv_pairing.html (%s): missing translation for %s", lang, key)
					}
				}
			}
			if name == "portal_tv.html" {
				want := map[string]string{"en": "Pair a TV", "id": "Pasangkan TV"}[lang]
				if !contains(out, want) {
					t.Errorf("portal_tv.html (%s): missing the pairing heading %q", lang, want)
				}
				// t() echoes a missing key, so a literal key in element text is
				// a translation leak on the counter's own page.
				for _, key := range []string{
					"tv.pr.pair", "tv.pr.sub", "tv.pr.code", "tv.pr.loc", "tv.pr.label",
					"tv.pr.submit", "tv.pr.list", "tv.pr.seen", "tv.pr.never",
					"tv.pr.rev", "tv.pr.revx", "p.nav.ptv",
				} {
					if contains(out, ">"+key+"<") {
						t.Errorf("portal_tv.html (%s): missing translation for %s", lang, key)
					}
				}
				if !contains(out, `href="/portal/tv"`) {
					t.Errorf("portal_tv.html (%s): missing the portal nav link", lang)
				}
			}
			if name == "tv_view.html" {
				want := map[string]string{"en": "Now serving", "id": "Sedang dilayani"}[lang]
				if !contains(out, want) {
					t.Errorf("tv_view.html (%s): missing the now-band heading %q", lang, want)
				}
				if !contains(out, "Sari W.") || !contains(out, "Pijat Balinese") {
					t.Errorf("tv_view.html (%s): queue rows are not rendered", lang)
				}
				// t() echoes a missing key, so a literal key is a translation
				// leak. data-status carries the key by design, hence the
				// element-text shape of the check.
				for _, key := range []string{
					"tv.title", "tv.live", "tv.now.h", "tv.next.h", "tv.day.h",
					"tv.now.empty", "tv.nxt.empty", "tv.day.empty",
					"tv.st.conf", "tv.st.arr", "tv.st.svc", "tv.st.done",
					"tv.sl.svc", "tv.sl.tm", "tv.sl.food", "tv.sl.rev", "tv.sl.revx",
				} {
					if contains(out, ">"+key+"<") {
						t.Errorf("tv_view.html (%s): missing translation for %s", lang, key)
					}
				}
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
			if name == "admin_payroll_rates.html" {
				// The rate page renders the form, the current rate and the full
				// timeline in both languages; a key in element text is a leak.
				want := map[string]string{"en": "Rates", "id": "Tarif"}[lang]
				if !contains(out, want) {
					t.Errorf("admin_payroll_rates.html (%s): missing the page title %q", lang, want)
				}
				for _, key := range []string{
					"pr.rates", "pr.ratesub", "pr.newrate", "pr.staff", "pr.pct", "pr.fixed",
					"pr.cad", "pr.base", "pr.eff", "pr.cur", "pr.hist", "pr.created", "pr.save",
					"pr.rateok", "pr.pct.bad", "pr.monthly", "pr.norate",
				} {
					if contains(out, ">"+key+"<") {
						t.Errorf("admin_payroll_rates.html (%s): missing translation for %s", lang, key)
					}
				}
				if !contains(out, "Rina Admin") {
					t.Errorf("admin_payroll_rates.html (%s): the creator name is not rendered", lang)
				}
			}
			if name == "admin_payroll.html" {
				// The payroll page is rendered in both languages with the full
				// representative period: period label, the note for an omitted
				// monthly fixed, and no raw dictionary key in element text.
				if !contains(out, payrollPeriodLabel(lang, payrollTestPeriod(service.PayrollKindWeek))) {
					t.Errorf("admin_payroll.html (%s): missing the period label", lang)
				}
				for _, key := range []string{"pr.miss.w", "pr.fixed.mo", "pr.paid", "pr.unpaid", "pr.rule"} {
					if contains(out, ">"+key+"<") {
						t.Errorf("admin_payroll.html (%s): missing translation for %s", lang, key)
					}
				}
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
				"customer_search_rows.html", "tv_queue_rows.html", "tv_queue_panel.html",
				"tv_slides.html", "tv_slide_services.html", "tv_slide_testimonials.html",
				"tv_slide_food.html", "tv_slide_review.html",
			} {
				// The queue row only renders its action controls when it has a
				// booking; feed the sweep every conditional block.
				data := pageData{"Lang": lang}
				if name == "portal_queue_rows.html" {
					data = queueSweepData(lang)
				}
				if strings.HasPrefix(name, "tv_") {
					data = tvSweepData(lang)
				}
				// Sweep the pairing poll's dead-code branch: its copy is the
				// only place tv.pr.exp/tv.pr.again render.
				if name == "tv_pair_state.html" {
					data["State"] = "expired"
				}
				if name == "portal_booking_rows.html" {
					// Empty page + pager data renders the empty state and every
					// pager label, so the key-leak scan below covers p.pg.*.
					data = pageData{
						"Lang": lang, "Page": 2, "TotalPages": 2,
						"PrevURL": "/portal/bookings", "NextURL": "/portal/bookings?page=3",
					}
				}
				var buf bytes.Buffer
				if err := s.tpl.ExecuteTemplate(&buf, name, data); err != nil {
					t.Errorf("execute partial %s (%s): %v", name, lang, err)
					continue
				}
				out := buf.String()
				for _, key := range []string{
					"slots.title", "slots.hint", "svc2.service", "pay.wait", "pay.status",
					"book.legend", "book.no_slots", "p.q.mt", "p.br.mt", "p.clm.n",
					"p.clm.mt", "p.clm.nr", "p.det.cff", "p.undo.l",
					"p.pg.x", "p.pg.prev", "p.pg.next",
					"tv.pr.exp", "tv.pr.again",
				} {
					if contains(out, `"`+key+`"`) || contains(out, ">"+key+"<") {
						t.Errorf("partial %s (%s): missing translation for key %s", name, lang, key)
					}
				}
			}
		}
	}
	// The pay page promises the slot is confirmed while payment is pending.
	// pay.html carries the promise for the manual (qris_manual) flow and
	// pay_status.html for the gateway flow, so each render shows it once and it
	// is gone once the payment is paid.
	payLine := map[string]string{
		"en": "Your slot is confirmed",
		"id": "Slot Anda sudah terkonfirmasi",
	}
	countPayLine := func(name string, data pageData, lang string) int {
		t.Helper()
		var buf bytes.Buffer
		if err := s.tpl.ExecuteTemplate(&buf, name, data); err != nil {
			t.Errorf("execute %s (%s): %v", name, lang, err)
			return -1
		}
		return strings.Count(buf.String(), payLine[lang])
	}
	for _, lang := range []string{"en", "id"} {
		contact := pageData{
			"WhatsApp": "62800", "Email": "a@b.c",
			"Address": "addr", "Hours": "hours", "Maps": "https://maps.example/",
			"Instagram": "https://instagram.example/", "TikTok": "https://tiktok.example/", "Facebook": "https://facebook.example/",
		}
		manual := pageData{
			"Lang":    lang,
			"Contact": contact,
			"Manual":  true,
			"Booking": &domain.Booking{StartAt: time.Now(), EndAt: time.Now().Add(time.Hour), Status: domain.BookingConfirmed},
			"Payment": &domain.Payment{Status: domain.PaymentPending, AmountIDR: 150000, OrderID: "WGG-TEST"},
		}
		if got := countPayLine("pay.html", manual, lang); got != 1 {
			t.Errorf("pay.html (%s, manual): slot-confirmed line appears %d times, want 1", lang, got)
		}
		// A customer who already claimed the transfer sees the wait state, not
		// the "pay now by QRIS or at the counter" promise: the QR block (and the
		// pay.conf line inside it) is hidden.
		reportedAt := time.Now()
		claimed := pageData{
			"Lang":    lang,
			"Contact": contact,
			"Manual":  true,
			"Claimed": true,
			"Booking": &domain.Booking{StartAt: time.Now(), EndAt: time.Now().Add(time.Hour), Status: domain.BookingConfirmed},
			"Payment": &domain.Payment{Status: domain.PaymentPending, AmountIDR: 150000, OrderID: "WGG-TEST", CustomerReportedAt: &reportedAt},
		}
		if got := countPayLine("pay.html", claimed, lang); got != 0 {
			t.Errorf("pay.html (%s, claimed): slot-confirmed promise must be hidden, appears %d times", lang, got)
		}
		gateway := pageData{
			"Lang":    lang,
			"Contact": contact,
			"Booking": &domain.Booking{StartAt: time.Now(), EndAt: time.Now().Add(time.Hour), Status: domain.BookingPendingPayment},
			"Payment": &domain.Payment{Status: domain.PaymentPending, AmountIDR: 150000, OrderID: "WGG-TEST"},
		}
		if got := countPayLine("pay.html", gateway, lang); got != 1 {
			t.Errorf("pay.html (%s, gateway): slot-confirmed line appears %d times, want 1", lang, got)
		}
		paid := pageData{
			"Lang":    lang,
			"Contact": contact,
			"Booking": &domain.Booking{Status: domain.BookingConfirmed},
			"Payment": &domain.Payment{Status: domain.PaymentPaid},
		}
		if got := countPayLine("pay_status.html", paid, lang); got != 0 {
			t.Errorf("pay_status.html (%s): slot-confirmed line must be gone once paid, appears %d times", lang, got)
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
// uiEmoji is the glyph list the UI-chrome scan forbids: icons are inline SVG
// ({{icon}}), never emoji. Shared so the reports tab sweep can scan every panel
// (only one panel renders per request) with the same list.
var uiEmoji = []string{
	"🏠", "👶", "👩", "💬", "📍", "📞", "💆", "🏥", "📝", "📊", "📅", "➕",
	"❌", "✅", "⚙", "✨", "🏢", "💼", "💵", "📱", "📈", "🖨", "🔑", "👥",
	"🤰", "🍽", "🖼", "🎟", "👑", "⚠", "🆕", "✉", "📸", "🕒", "💡", "⬇",
}

func TestNoEmojiIcons(t *testing.T) {
	forbidden := uiEmoji
	pages := []string{
		"home.html", "services.html", "book.html", "bookings.html", "login.html",
		"pay.html", "register.html", "careers.html", "testimonial_new.html", "account.html", "food.html", "gallery.html",
		"admin_home.html", "admin_payments.html", "admin_locations.html", "admin_testimonials.html", "admin_whatsapp.html",
		"admin_tv.html", "admin_payroll.html", "admin_payroll_rates.html", "tv_invalid.html", "tv_view.html", "tv_pairing.html",
		"admin_cs.html", "admin_customer_reset.html", "admin_password.html", "admin_jobs.html",
		"admin_job_applications.html", "portal_dashboard.html", "portal_bookings.html",
		"portal_booking_new.html", "portal_booking_detail.html", "portal_customers.html",
		"portal_customer_new.html", "portal_customer_detail.html", "portal_customer_edit.html", "portal_schedule.html",
		"portal_services.html", "portal_staff_hours.html", "portal_staff_edit.html",
		"portal_service_edit.html", "portal_reports.html", "portal_food.html", "portal_food_edit.html", "portal_gallery.html", "portal_gallery_edit.html", "portal_site.html", "portal_promos.html", "portal_distance.html", "portal_tv.html", "portal_error.html",
	}
	partials := []string{
		"services_partial.html", "staff_partial.html", "slots_partial.html",
		"pay_status.html", "toast.html", "service_cards.html", "staff_cards.html",
		"slot_groups.html", "portal_queue_rows.html", "portal_booking_rows.html",
		"customer_search_rows.html", "promo_preview.html", "tv_queue_rows.html",
		"tv_queue_panel.html", "tv_pair_state.html", "tv_slides.html", "tv_slide_services.html",
		"tv_slide_testimonials.html", "tv_slide_food.html", "tv_slide_review.html",
	}
	s, err := New(config.Config{}, slog.Default(), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
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
			case "tv_view.html":
				for k, v := range tvSweepData(lang) {
					data[k] = v
				}
			case "tv_pairing.html":
				data["Code"] = "482913"
			case "portal_tv.html":
				for k, v := range portalTVSweepData() {
					data[k] = v
				}
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
			case "admin_whatsapp.html":
				data["Settings"] = service.WaConfig{}
				data["Raw"] = &domain.WaSettings{}
				data["Defaults"] = service.WaConfig{}
				data["Conn"] = "mock"
				data["Templates"] = []waTemplateView{{
					Key: "booking_remind", LabelKey: "a.wt.remind", Default: "Halo Bunda",
					Fields: []string{"name"}, Preview: "Halo Bunda",
				}}
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
				for k, v := range reportsSweepData("revenue") {
					data[k] = v
				}
			case "admin_payroll.html":
				for k, v := range payrollSweepData(lang) {
					data[k] = v
				}
			case "admin_payroll_rates.html":
				for k, v := range payrollRatesSweepData(lang) {
					data[k] = v
				}
			case "admin_tv.html":
				// One live token so the sweep covers the URL block, the branch
				// name, the revoke form and the create form, not just the empty
				// state.
				locID := uuid.New()
				data["BaseURL"] = seoTestBaseURL
				data["Tokens"] = []domain.TVToken{{
					ID: uuid.New(), Token: "tok", Label: "Lobby TV",
					LocationID: locID, CreatedAt: &tvSweepTime,
				}}
				data["Locations"] = []domain.Location{{ID: locID, Name: "Kayu Putih", Active: true}}
				data["LocNames"] = map[string]string{locID.String(): "Kayu Putih"}
				data["Devices"] = []domain.TVDevice{{
					ID: uuid.New(), Label: "Lobby TV", LocationID: locID, PairedAt: &tvSweepTime,
				}}
			}
			var buf bytes.Buffer
			if err := s.tpl.ExecuteTemplate(&buf, name, data); err != nil {
				t.Errorf("execute %s (%s): %v", name, lang, err)
				continue
			}
			check(name, lang, buf.String())
		}
		for _, name := range partials {
			// Feed the queue row its conditional blocks; an empty render would
			// only scan the empty-state cell.
			data := pageData{"Lang": lang}
			if name == "portal_queue_rows.html" {
				data = queueSweepData(lang)
			}
			if strings.HasPrefix(name, "tv_") {
				data = tvSweepData(lang)
			}
			var buf bytes.Buffer
			if err := s.tpl.ExecuteTemplate(&buf, name, data); err != nil {
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
