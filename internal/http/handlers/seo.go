package handlers

import (
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"spa-booking/internal/domain"
)

// seoPublicPaths is the sitemap inventory. Public pages only: no auth'd URLs,
// no ?t= guest tokens, no uploads. /testimonials/new belongs here because the
// submission form becomes guest-accessible in this same branch.
var seoPublicPaths = []string{
	"/",
	"/services",
	"/gallery",
	"/book",
	"/food",
	"/careers",
	"/testimonials/new",
}

// robotsTXTBody renders the crawler policy: allow-by-default for every agent
// (search and AI alike), Disallow only the private app areas, and signal
// search/grounding yes but training no. Pure: no config, no clock.
func robotsTXTBody(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	var b strings.Builder
	b.WriteString("# robots.txt for Wangga Spa.\n")
	b.WriteString("# Public storefront pages are open to search engines and AI assistants;\n")
	b.WriteString("# staff areas and uploads are not.\n")
	b.WriteString("# Content-Signal: search=yes, ai-input=yes, ai-train=no, use=immediate\n")
	b.WriteString("\n")
	b.WriteString("User-agent: *\n")
	b.WriteString("Allow: /\n")
	b.WriteString("Disallow: /portal\n")
	b.WriteString("Disallow: /admin\n")
	b.WriteString("Disallow: /bookings\n")
	b.WriteString("Disallow: /uploads/\n")
	b.WriteString("\n")
	b.WriteString("Sitemap: " + base + "/sitemap.xml\n")
	return b.String()
}

// sitemapXMLBody renders the public page inventory. No lastmod: the file is
// static and an invented date would go stale.
func sitemapXMLBody(baseURL string) string {
	base := strings.TrimRight(baseURL, "/")
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, p := range seoPublicPaths {
		b.WriteString("  <url><loc>" + base + p + "</loc></url>\n")
	}
	b.WriteString("</urlset>\n")
	return b.String()
}

// llmsTXTBody renders the plain-English fact sheet for AI assistants. Prices,
// names, and durations come from the passed catalog groups; nothing is
// invented. Addresses come from the passed locations, the phone from the
// caller (config CONTACT_WHATSAPP).
func llmsTXTBody(baseURL, hours, waPhone string, locs []domain.Location, groups []locationServices) string {
	base := strings.TrimRight(baseURL, "/")
	var b strings.Builder
	b.WriteString("# Wangga Spa — Baby, Mom & Woman Spa\n\n")
	b.WriteString("> Wangga Spa is a spa for babies, mothers, and women in Kayu Putih, ")
	b.WriteString("Pulo Gadung, East Jakarta, Indonesia. Treatments are offered at the studio ")
	b.WriteString("and as homecare visits.\n\n")
	b.WriteString("This file is a plain-text fact sheet for AI assistants and answer engines. ")
	b.WriteString("Prices, durations, and hours mirror the live catalog served on the website.\n\n")

	b.WriteString("## Key pages\n\n")
	b.WriteString("- Website: " + base + "/\n")
	b.WriteString("- Services and prices: " + base + "/services\n")
	b.WriteString("- Book online: " + base + "/book\n")
	b.WriteString("- Gallery: " + base + "/gallery\n")
	b.WriteString("- Food and drinks: " + base + "/food\n")
	b.WriteString("- Careers: " + base + "/careers\n\n")

	b.WriteString("## Locations\n\n")
	if len(locs) == 0 {
		b.WriteString("- See " + base + "/services for the current branch list.\n\n")
	}
	for _, l := range locs {
		b.WriteString("### " + l.Name + "\n\n")
		addr := strings.TrimSpace(l.Address)
		if city := strings.TrimSpace(l.City); city != "" {
			if addr != "" {
				addr += ", "
			}
			addr += city
		}
		if addr != "" {
			b.WriteString("- Address: " + addr + "\n")
			b.WriteString("- Directions: " + mapsSearchURL(addr) + "\n")
		}
		if hours != "" {
			b.WriteString("- Opening hours: " + hours + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("## Services\n\n")
	wroteServices := false
	for _, g := range groups {
		if len(g.Services) == 0 {
			continue
		}
		wroteServices = true
		b.WriteString("### " + g.Location.Name + "\n\n")
		for _, svc := range g.Services {
			b.WriteString("- " + serviceFactLine(svc) + "\n")
		}
		b.WriteString("\n")
	}
	if !wroteServices {
		b.WriteString("- See " + base + "/services for the current treatment list and prices.\n\n")
	}

	b.WriteString("## Homecare visits\n\n")
	b.WriteString("Wangga Spa can send a therapist to your home. In the booking wizard choose ")
	b.WriteString("Homecare, enter the visit address, and open the Google Maps directions link ")
	b.WriteString("to measure the one-way distance; then pick the matching distance band. ")
	b.WriteString("The transport fee is by distance band, shown before you book.\n\n")

	b.WriteString("## Food and drinks\n\n")
	b.WriteString("Bu Nah's kitchen at the spa serves food, beverages, and snacks. Order at the ")
	b.WriteString("counter or by WhatsApp; food is paid onsite and never through the website.\n\n")

	b.WriteString("## How to book\n\n")
	b.WriteString("1. Open " + base + "/book.\n")
	b.WriteString("2. Pick a branch, a treatment, a therapist, and a time.\n")
	b.WriteString("3. Choose Onsite or Homecare; for homecare, add the visit address and distance band.\n")
	b.WriteString("4. Review the price and confirm. You can book as a guest or sign in.\n")
	b.WriteString("5. Pay by QRIS now, or pay at the counter when you arrive.\n\n")
	b.WriteString("To book without the website, send a WhatsApp message or ask at the counter ")
	b.WriteString("during opening hours.\n\n")

	b.WriteString("## Payment\n\n")
	b.WriteString("- QRIS: scan the merchant QRIS shown after booking, either right away or at the counter.\n")
	b.WriteString("- Cash: at the counter for treatments only. Booking fees are never paid in cash.\n\n")

	b.WriteString("## Languages\n\n")
	b.WriteString("Indonesian (default) and English. The site, the booking wizard, and the ")
	b.WriteString("WhatsApp messages follow the chosen language.\n\n")

	b.WriteString("## Contact\n\n")
	if waPhone != "" {
		b.WriteString("- WhatsApp and phone: https://wa.me/" + waPhone + "\n")
	}
	return b.String()
}

// mapsSearchURL builds a Google Maps search link from a text address, so every
// branch gets its own link without a hardcoded place URL.
func mapsSearchURL(address string) string {
	return "https://www.google.com/maps/search/?api=1&query=" + url.QueryEscape(address)
}

// serviceFactLine renders "Name (NameID) — Rp 150.000, 45 min"; the Indonesian
// name appears only when the catalog carries one.
func serviceFactLine(svc domain.Service) string {
	name := svc.Name
	if svc.NameID != "" && svc.NameID != svc.Name {
		name += " (" + svc.NameID + ")"
	}
	return name + " — " + rupiahIDR(svc.PriceIDR) + ", " + strconv.Itoa(svc.DurationMin) + " min"
}

// rupiahIDR formats 150000 as "Rp 150.000".
func rupiahIDR(n int64) string {
	s := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(c)
	}
	return "Rp " + b.String()
}

// RobotsTXT serves the crawler policy.
func (s *Server) RobotsTXT(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = io.WriteString(w, robotsTXTBody(s.cfg.BaseURL))
}

// SitemapXML serves the public page inventory.
func (s *Server) SitemapXML(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = io.WriteString(w, sitemapXMLBody(s.cfg.BaseURL))
}

// LLMSTxt serves the plain-English fact sheet, built from the live catalog.
func (s *Server) LLMSTxt(w http.ResponseWriter, r *http.Request) {
	locs, err := s.catalog.ListLocations(r.Context(), true)
	if err != nil {
		s.log.Error("llms.txt: list locations", "err", err)
		http.Error(w, "failed to load locations", http.StatusInternalServerError)
		return
	}
	groups := make([]locationServices, 0, len(locs))
	for _, l := range locs {
		svcs, err := s.catalog.ListServices(r.Context(), l.ID, false)
		if err != nil {
			s.log.Error("llms.txt: list services", "err", err)
			http.Error(w, "failed to load services", http.StatusInternalServerError)
			return
		}
		groups = append(groups, locationServices{Location: l, Services: svcs})
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = io.WriteString(w, llmsTXTBody(s.cfg.BaseURL, s.cfg.ContactHours, s.cfg.ContactWhatsApp, locs, groups))
}
