package handlers

import (
	"encoding/json"
	"fmt"
	"html/template"
	"regexp"
	"strconv"
	"strings"

	"spa-booking/internal/domain"
	"spa-booking/internal/i18n"
)

const (
	// seoPriceRange is the published treatment band, cheapest to priciest
	// catalog entry. Crawlers show it as the business price range.
	seoPriceRange = "Rp125.000 - Rp200.000"
	// seoOpeningHours is the schema.org form of the daily studio hours. The
	// wall-clock window follows CONTACT_HOURS when it carries a parsable range.
	seoOpeningHours = "Mo-Su 11:00-21:00"
	// seoBrandName is the public brand line used in titles and legal names.
	seoBrandName = "Wangga Spa Baby, Mom, & Woman"
	// faqCount is the number of Q&A pairs on the home page: faq.q1..faq.qN and
	// faq.a1..faq.aN feed both the visible accordion and the JSON-LD graph.
	faqCount = 5
	// seoLogoPath is the business logo and the Open Graph preview image.
	seoLogoPath = "/static/img/logo.png"
)

// seoHoursRange matches the wall-clock window inside a human hours string
// such as "Setiap Hari 11:00–21:00 WIB" (en dash, hyphen, or em dash).
var seoHoursRange = regexp.MustCompile(`(\d{1,2})[:.](\d{2})\s*[–\-—]\s*(\d{1,2})[:.](\d{2})`)

// openingHoursValue converts the human hours string into the schema.org form.
// The spa opens every day, so only the time window is read from the string;
// text without a parsable range falls back to the documented default instead
// of emitting a malformed value.
func openingHoursValue(hours string) string {
	m := seoHoursRange.FindStringSubmatch(hours)
	if m == nil {
		return seoOpeningHours
	}
	return "Mo-Su " + m[1] + ":" + m[2] + "-" + m[3] + ":" + m[4]
}

// seoSlug reduces a location name to an @id fragment.
func seoSlug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			dash = false
		case !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// siteStructuredData renders one HealthAndBeautyBusiness per branch. Nothing
// here is invented: the address comes from the location row, the phone from
// config CONTACT_WHATSAPP, and the social profiles from config CONTACT_*.
// With a single branch the site itself is the entity (@id = baseURL); with
// several, each block gets its own anchor so no two nodes share an @id.
func siteStructuredData(baseURL, hours, waPhone string, locs []domain.Location, sameAs []string) []map[string]any {
	base := strings.TrimRight(baseURL, "/")
	open := openingHoursValue(hours)
	blocks := make([]map[string]any, 0, len(locs))
	for i, loc := range locs {
		id := base
		if len(locs) > 1 {
			slug := seoSlug(loc.Name)
			if slug == "" {
				slug = "location-" + strconv.Itoa(i+1)
			}
			id = base + "#" + slug
		}
		block := map[string]any{
			"@type":        "HealthAndBeautyBusiness",
			"@id":          id,
			"name":         seoBrandName + " — " + loc.Name,
			"url":          base + "/",
			"image":        base + seoLogoPath,
			"priceRange":   seoPriceRange,
			"openingHours": open,
			"address": map[string]any{
				"@type":           "PostalAddress",
				"streetAddress":   strings.TrimSpace(loc.Address),
				"addressLocality": strings.TrimSpace(loc.City),
				"addressCountry":  "ID",
			},
		}
		if phone := strings.TrimSpace(waPhone); phone != "" {
			block["telephone"] = phone
		}
		if len(sameAs) > 0 {
			block["sameAs"] = sameAs
		}
		blocks = append(blocks, block)
	}
	return blocks
}

// servicesStructuredData renders the bookable catalog as an ItemList of
// Service/Offer pairs. Prices stay int64 so encoding/json writes them as bare
// numbers (never quoted strings), the currency is IDR, and the duration rides
// along in the description. Inactive entries are not bookable and are skipped.
func servicesStructuredData(baseURL string, groups []locationServices) map[string]any {
	base := strings.TrimRight(baseURL, "/")
	svcURL := base + "/services"
	items := make([]map[string]any, 0)
	for _, g := range groups {
		for _, svc := range g.Services {
			if !svc.Active {
				continue
			}
			desc := strings.TrimSpace(svc.Description)
			duration := strconv.Itoa(svc.DurationMin) + " min"
			if desc == "" {
				desc = duration
			} else {
				desc += " — " + duration
			}
			items = append(items, map[string]any{
				"@type":    "ListItem",
				"position": len(items) + 1,
				"item": map[string]any{
					"@type":       "Service",
					"name":        svc.Name,
					"description": desc,
					"url":         svcURL,
					"provider": map[string]any{
						"@type": "HealthAndBeautyBusiness",
						"name":  seoBrandName + " — " + g.Location.Name,
					},
					"offers": map[string]any{
						"@type":         "Offer",
						"price":         svc.PriceIDR,
						"priceCurrency": "IDR",
						"availability":  "https://schema.org/InStock",
						"url":           svcURL,
					},
				},
			})
		}
	}
	return map[string]any{
		"@type":           "ItemList",
		"name":            seoBrandName + " treatments",
		"numberOfItems":   len(items),
		"itemListElement": items,
	}
}

// faqStructuredData mirrors the visible home-page FAQ into a FAQPage node.
// Both read the same dictionary keys, so the machine copy can never drift from
// the text a visitor sees (search engines require the match).
func faqStructuredData(lang string) map[string]any {
	qa := make([]map[string]any, 0, faqCount)
	for n := 1; n <= faqCount; n++ {
		qa = append(qa, map[string]any{
			"@type": "Question",
			"name":  i18n.T(lang, fmt.Sprintf("faq.q%d", n)),
			"acceptedAnswer": map[string]any{
				"@type": "Answer",
				"text":  i18n.T(lang, fmt.Sprintf("faq.a%d", n)),
			},
		})
	}
	return map[string]any{"@type": "FAQPage", "mainEntity": qa}
}

// jsonldHTML serialises the passed blocks into one @graph script element.
// Each argument may be a []map[string]any (flattened) or a single block.
// Safe as template.HTML: encoding/json escapes <, >, and &, so no payload can
// close the script element.
func jsonldHTML(blocks ...any) template.HTML {
	graph := make([]any, 0, len(blocks))
	for _, b := range blocks {
		switch v := b.(type) {
		case nil:
			continue
		case []map[string]any:
			for _, node := range v {
				graph = append(graph, node)
			}
		default:
			graph = append(graph, v)
		}
	}
	payload := map[string]any{
		"@context": "https://schema.org",
		"@graph":   graph,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return template.HTML(`<script type="application/ld+json">` + string(b) + `</script>`)
}

// seoHead builds the per-page head meta: the shared site description plus the
// canonical path and the Open Graph title. BaseURL comes from render().
func seoHead(lang, title, path string) pageData {
	return pageData{
		"Title":       title,
		"Description": i18n.T(lang, "seo.desc"),
		"Path":        path,
	}
}

// siteStructuredBlocks gathers the shared inputs for the business graph.
func (s *Server) siteStructuredBlocks(locs []domain.Location) []map[string]any {
	return siteStructuredData(s.cfg.BaseURL, s.cfg.ContactHours, s.cfg.ContactWhatsApp, locs, s.sameAsLinks())
}

// sameAsLinks lists the public social profiles; unset config values drop out
// rather than serialising empty strings.
func (s *Server) sameAsLinks() []string {
	var out []string
	for _, u := range []string{s.cfg.ContactIG, s.cfg.ContactTikTok, s.cfg.ContactFB} {
		if u = strings.TrimSpace(u); u != "" {
			out = append(out, u)
		}
	}
	return out
}
