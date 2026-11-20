package handlers

import (
	"strings"
	"testing"

	"spa-booking/internal/domain"
)

// seoTestBaseURL is the verified production value (https://wangga.biz.id).
const seoTestBaseURL = "https://wangga.biz.id"

// privatePathMarkers must never appear in the sitemap or the llms.txt body:
// they name staff-only areas or guest payment tokens.
var privatePathMarkers = []string{"/portal", "/admin", "/bookings", "/uploads/", "?t="}

// seoTestWhatsApp is the fixture phone threaded into llmsTXTBody; the body
// must render whatever the handler passes (config CONTACT_WHATSAPP), never a
// value baked into seo.go.
const seoTestWhatsApp = "628211347294"

func seoFixtures() (string, []domain.Location, []locationServices) {
	hours := "Setiap Hari 11:00–21:00 WIB"
	locs := []domain.Location{{
		Name:    "Wangga Spa Kayu Putih",
		Address: "Studio Kayu Putih No.32",
		City:    "Jakarta Timur",
		Active:  true,
	}}
	groups := []locationServices{{
		Location: locs[0],
		Services: []domain.Service{
			{Name: "Baby Massage", NameID: "Pijat Bayi", DurationMin: 45, PriceIDR: 150000, Active: true},
			{Name: "Mom Massage", DurationMin: 60, PriceIDR: 200000, Active: true},
			// Paused catalog entry: not bookable, so never advertised with a price.
			{Name: "Waxing", DurationMin: 30, PriceIDR: 90000, Active: false},
		},
	}}
	return hours, locs, groups
}

// TestSEOBodies is the table-driven guard over the three machine-readable
// bodies. The private-URL absences are the point of the task: a crawler must
// never learn /portal, /admin, /bookings, /uploads/ or a guest ?t= URL.
func TestSEOBodies(t *testing.T) {
	hours, locs, groups := seoFixtures()

	cases := []struct {
		name     string
		body     string
		contains []string
		absent   []string
	}{
		{
			name: "robots.txt",
			body: robotsTXTBody(seoTestBaseURL),
			contains: []string{
				"User-agent: *",
				"Allow: /",
				"Disallow: /portal",
				"Disallow: /admin",
				"Disallow: /bookings",
				"Disallow: /uploads/",
				"Content-Signal: search=yes",
				"ai-input=yes",
				"ai-train=no",
				"Sitemap: " + seoTestBaseURL + "/sitemap.xml",
			},
			absent: []string{
				"GPTBot", "ClaudeBot", "Google-Extended", "PerplexityBot",
				"CCBot", "anthropic-ai", "cohere-ai",
			},
		},
		{
			name: "sitemap.xml",
			body: sitemapXMLBody(seoTestBaseURL),
			contains: []string{
				`xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"`,
				"<loc>" + seoTestBaseURL + "/</loc>",
				"<loc>" + seoTestBaseURL + "/services</loc>",
				"<loc>" + seoTestBaseURL + "/gallery</loc>",
				"<loc>" + seoTestBaseURL + "/book</loc>",
				"<loc>" + seoTestBaseURL + "/food</loc>",
				"<loc>" + seoTestBaseURL + "/careers</loc>",
				"<loc>" + seoTestBaseURL + "/testimonials/new</loc>",
			},
			absent: privatePathMarkers,
		},
		{
			name: "llms.txt",
			body: llmsTXTBody(seoTestBaseURL, hours, seoTestWhatsApp, locs, groups),
			contains: []string{
				"Wangga Spa",
				"Studio Kayu Putih No.32",
				"Jakarta Timur",
				// Derived per location, not a hardcoded place URL: the
				// encoded address+city must be in the query.
				"https://www.google.com/maps/search/?api=1&query=Studio+Kayu+Putih+No.32%2C+Jakarta+Timur",
				"Baby Massage (Pijat Bayi)",
				"Rp 150.000",
				"45 min",
				"Mom Massage",
				"Rp 200.000",
				"60 min",
				"628211347294",
				hours,
				"## How to book",
				"/book",
				"distance band, shown before you book",
			},
			absent: append(privatePathMarkers, "Waxing", "Rp 90.000"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, want := range tc.contains {
				if !strings.Contains(tc.body, want) {
					t.Errorf("%s body missing %q", tc.name, want)
				}
			}
			for _, gone := range tc.absent {
				if strings.Contains(tc.body, gone) {
					t.Errorf("%s body must not contain %q", tc.name, gone)
				}
			}
			if strings.TrimSpace(tc.body) == "" {
				t.Fatalf("%s body is empty", tc.name)
			}
		})
	}
}

// TestRobotsTXTNoBlanketDisallow pins the allow-by-default policy: the only
// blanket rule is Allow: /, the four private Disallows are the exact set, and
// there is a single user-agent group (no per-bot rules).
func TestRobotsTXTNoBlanketDisallow(t *testing.T) {
	body := robotsTXTBody(seoTestBaseURL)
	if strings.Count(body, "User-agent:") != 1 {
		t.Errorf("robots.txt must have exactly one user-agent group, got %d", strings.Count(body, "User-agent:"))
	}
	var disallows []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Disallow: ") {
			continue
		}
		path := strings.TrimPrefix(line, "Disallow: ")
		if path == "/" {
			t.Fatalf("robots.txt has a blanket Disallow: / line")
		}
		disallows = append(disallows, path)
	}
	want := []string{"/portal", "/admin", "/bookings", "/uploads/"}
	if strings.Join(disallows, ",") != strings.Join(want, ",") {
		t.Errorf("robots.txt Disallow set = %v, want %v", disallows, want)
	}
}

// TestLLMSTXTBodyUsesPassedPhone pins the fix for the baked-in contact: the
// phone line must render the caller's value (config CONTACT_WHATSAPP), and a
// different passed number must displace the production one.
func TestLLMSTXTBodyUsesPassedPhone(t *testing.T) {
	hours, locs, groups := seoFixtures()
	const phone = "620000000001"
	body := llmsTXTBody(seoTestBaseURL, hours, phone, locs, groups)
	if !strings.Contains(body, "https://wa.me/"+phone) {
		t.Errorf("llms.txt does not render the passed phone %q", phone)
	}
	if strings.Contains(body, seoTestWhatsApp) {
		t.Errorf("llms.txt leaked a phone number not passed by the caller")
	}
}

// TestSitemapXMLBodyOmitsLastmod keeps the file static: no lastmod means no
// stale timestamps to maintain.
func TestSitemapXMLBodyOmitsLastmod(t *testing.T) {
	if strings.Contains(sitemapXMLBody(seoTestBaseURL), "lastmod") {
		t.Error("sitemap.xml must not emit lastmod")
	}
}
