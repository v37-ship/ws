package handlers

import (
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	"strings"
	"testing"

	"spa-booking/internal/config"
	"spa-booking/internal/domain"
	"spa-booking/internal/i18n"
)

// The structured-data facts verified for this task: daily 11:00-21:00 hours
// and the published price band. They are asserted verbatim so a careless edit
// to the builders cannot quietly change what search engines read.
const (
	wantOpeningHours = "Mo-Su 11:00-21:00"
	wantPriceRange   = "Rp125.000 - Rp200.000"
)

func sdLocations() []domain.Location {
	return []domain.Location{{
		Name:    "Wangga Spa Kayu Putih",
		Address: "Studio Kayu Putih No.32",
		City:    "Jakarta Timur",
		Active:  true,
	}}
}

func sdGroups() []locationServices {
	locs := sdLocations()
	return []locationServices{{
		Location: locs[0],
		Services: []domain.Service{
			{Name: "Baby Massage", NameID: "Pijat Bayi", Description: "Gentle massage for babies", DurationMin: 45, PriceIDR: 150000, Active: true},
			{Name: "Mom Massage", DurationMin: 60, PriceIDR: 200000, Active: true},
		},
	}}
}

func marshalSD(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal structured data: %v", err)
	}
	return string(b)
}

// TestSameAsLinks pins the social list: the configured profiles plus the
// Google review page, with unset values dropped instead of serialised empty.
func TestSameAsLinks(t *testing.T) {
	s := &Server{cfg: config.Config{
		ContactIG:       " https://www.instagram.com/wanggababymomwoman/ ",
		GoogleReviewURL: "https://g.page/r/CTLPrFudX25eEBM/review",
	}}
	want := []string{
		"https://www.instagram.com/wanggababymomwoman/",
		"https://g.page/r/CTLPrFudX25eEBM/review",
	}
	if got := s.sameAsLinks(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("sameAsLinks() = %v, want %v", got, want)
	}
	if got := (&Server{}).sameAsLinks(); len(got) != 0 {
		t.Errorf("sameAsLinks() with empty config = %v, want none", got)
	}
}

func TestSiteStructuredData(t *testing.T) {
	blocks := siteStructuredData(seoTestBaseURL, "Setiap Hari 11:00–21:00 WIB", seoTestWhatsApp, sdLocations(), []string{"https://www.instagram.com/wanggababymomwoman/"})
	if len(blocks) != 1 {
		t.Fatalf("one active location should yield one block, got %d", len(blocks))
	}
	b := blocks[0]
	if got := b["@type"]; got != "HealthAndBeautyBusiness" {
		t.Errorf("@type = %v, want HealthAndBeautyBusiness", got)
	}
	// Single branch: the site itself is the entity.
	if got := b["@id"]; got != seoTestBaseURL {
		t.Errorf("@id = %v, want %s", got, seoTestBaseURL)
	}
	if got, _ := b["name"].(string); !strings.Contains(got, "Wangga Spa Kayu Putih") {
		t.Errorf("name = %q, want it to carry the location name", got)
	}
	if got := b["telephone"]; got != seoTestWhatsApp {
		t.Errorf("telephone = %v, want %s", got, seoTestWhatsApp)
	}
	if got := b["openingHours"]; got != wantOpeningHours {
		t.Errorf("openingHours = %v, want %s", got, wantOpeningHours)
	}
	if got := b["priceRange"]; got != wantPriceRange {
		t.Errorf("priceRange = %v, want %s", got, wantPriceRange)
	}
	if got := b["url"]; got != seoTestBaseURL+"/" {
		t.Errorf("url = %v, want %s/", got, seoTestBaseURL)
	}
	sameAs, ok := b["sameAs"].([]string)
	if !ok || len(sameAs) != 1 {
		t.Errorf("sameAs = %v, want the one passed social URL", b["sameAs"])
	}
	addr, ok := b["address"].(map[string]any)
	if !ok {
		t.Fatalf("address is not an object: %T", b["address"])
	}
	if addr["@type"] != "PostalAddress" {
		t.Errorf("address @type = %v", addr["@type"])
	}
	if addr["streetAddress"] != "Studio Kayu Putih No.32" {
		t.Errorf("streetAddress = %v", addr["streetAddress"])
	}
	if addr["addressLocality"] != "Jakarta Timur" {
		t.Errorf("addressLocality = %v", addr["addressLocality"])
	}
	if addr["addressCountry"] != "ID" {
		t.Errorf("addressCountry = %v", addr["addressCountry"])
	}
	// The marshalled block must carry the literal values a crawler reads.
	raw := marshalSD(t, blocks)
	for _, want := range []string{
		`"@type":"HealthAndBeautyBusiness"`,
		`"openingHours":"` + wantOpeningHours + `"`,
		`"priceRange":"` + wantPriceRange + `"`,
		`"telephone":"` + seoTestWhatsApp + `"`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("site block missing %s", want)
		}
	}
}

// TestShareCardIsThePreviewImage keeps one card behind every consumer: the
// embedded asset really matches the og:image:width/height tags (so they cannot
// lie) and the LocalBusiness JSON-LD names the same file as the og meta.
func TestShareCardIsThePreviewImage(t *testing.T) {
	const wantPath = "/static/img/share-card.jpg"
	blocks := siteStructuredData(seoTestBaseURL, "", seoTestWhatsApp, sdLocations(), nil)
	if got := blocks[0]["image"]; got != seoTestBaseURL+wantPath {
		t.Errorf("JSON-LD image = %v, want %s", got, seoTestBaseURL+wantPath)
	}
	f, err := staticFS.Open(strings.TrimPrefix(wantPath, "/"))
	if err != nil {
		t.Fatalf("share card missing from the embedded static tree: %v", err)
	}
	defer f.Close()
	cfg, format, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatalf("decode share card: %v", err)
	}
	if format != "jpeg" {
		t.Errorf("share card format = %s, want jpeg", format)
	}
	if cfg.Width != 1080 || cfg.Height != 773 {
		t.Errorf("share card is %dx%d, want 1080x773 (og:image:width/height)", cfg.Width, cfg.Height)
	}
}

func TestSiteStructuredDataMultipleLocations(t *testing.T) {
	locs := []domain.Location{
		{Name: "Wangga Spa Kayu Putih", Address: "Studio Kayu Putih No.32", City: "Jakarta Timur", Active: true},
		{Name: "Wangga Spa Kemang", Address: "Jl. Kemang Raya No.1", City: "Jakarta Selatan", Active: true},
	}
	blocks := siteStructuredData(seoTestBaseURL, "", seoTestWhatsApp, locs, nil)
	if len(blocks) != 2 {
		t.Fatalf("two locations should yield two blocks, got %d", len(blocks))
	}
	ids := map[string]bool{}
	for _, b := range blocks {
		id, _ := b["@id"].(string)
		if !strings.HasPrefix(id, seoTestBaseURL) {
			t.Errorf("@id %q must be rooted at the base URL", id)
		}
		if ids[id] {
			t.Errorf("duplicate @id %q for distinct locations", id)
		}
		ids[id] = true
		// An empty hours string must not empty the field.
		if b["openingHours"] != wantOpeningHours {
			t.Errorf("openingHours = %v, want the default %s", b["openingHours"], wantOpeningHours)
		}
	}
	if blocks[0]["name"] == blocks[1]["name"] {
		t.Error("location names must differ between blocks")
	}
}

// TestSiteStructuredDataFollowsConfiguredHours keeps the machine hours honest
// when an admin edits CONTACT_HOURS: the time window is taken from the passed
// string, and an unparseable string falls back to the documented default.
func TestSiteStructuredDataFollowsConfiguredHours(t *testing.T) {
	cases := map[string]string{
		"Setiap Hari 11:00–21:00 WIB": wantOpeningHours,
		"Setiap Hari 10:00-20:00 WIB": "Mo-Su 10:00-20:00",
		"open daily 9:30 - 18:00":     "Mo-Su 9:30-18:00",
		"call us":                     wantOpeningHours,
		"":                            wantOpeningHours,
	}
	for in, want := range cases {
		blocks := siteStructuredData(seoTestBaseURL, in, seoTestWhatsApp, sdLocations(), nil)
		if got := blocks[0]["openingHours"]; got != want {
			t.Errorf("openingHours for %q = %v, want %s", in, got, want)
		}
	}
}

func TestServicesStructuredData(t *testing.T) {
	block := servicesStructuredData(seoTestBaseURL, sdGroups())
	if got := block["@type"]; got != "ItemList" {
		t.Fatalf("@type = %v, want ItemList", got)
	}
	items, ok := block["itemListElement"].([]map[string]any)
	if !ok {
		t.Fatalf("itemListElement is %T, want []map[string]any", block["itemListElement"])
	}
	if len(items) != 2 {
		t.Fatalf("two services should yield two list items, got %d", len(items))
	}
	for i, it := range items {
		if got := it["position"]; got != i+1 {
			t.Errorf("item %d position = %v, want %d", i, got, i+1)
		}
		svc, ok := it["item"].(map[string]any)
		if !ok {
			t.Fatalf("item %d payload is %T", i, it["item"])
		}
		if svc["@type"] != "Service" {
			t.Errorf("item %d @type = %v, want Service", i, svc["@type"])
		}
		offer, ok := svc["offers"].(map[string]any)
		if !ok {
			t.Fatalf("item %d has no offers object", i)
		}
		if offer["@type"] != "Offer" {
			t.Errorf("item %d offer @type = %v", i, offer["@type"])
		}
		if offer["priceCurrency"] != "IDR" {
			t.Errorf("item %d priceCurrency = %v, want IDR", i, offer["priceCurrency"])
		}
		if _, ok := offer["price"].(int64); !ok {
			t.Errorf("item %d price is %T, want int64", i, offer["price"])
		}
		if url, _ := offer["url"].(string); !strings.HasPrefix(url, seoTestBaseURL) {
			t.Errorf("item %d offer url = %q, want it rooted at the base URL", i, url)
		}
	}
	// Prices must marshal as bare integers, never quoted strings.
	raw := marshalSD(t, block)
	for _, want := range []string{
		`"price":150000`,
		`"price":200000`,
		`"priceCurrency":"IDR"`,
		`"@type":"Service"`,
		`"@type":"Offer"`,
		`45 min`,
		`60 min`,
		`"name":"Baby Massage"`,
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("services block missing %s\n%s", want, raw)
		}
	}
	if strings.Contains(raw, `"price":"`) {
		t.Errorf("price must be a bare number, got quoted prices:\n%s", raw)
	}
}

// Inactive catalog entries are not bookable, so they must not be advertised
// as an available Offer.
func TestServicesStructuredDataSkipsInactive(t *testing.T) {
	groups := sdGroups()
	groups[0].Services[0].Active = false
	block := servicesStructuredData(seoTestBaseURL, groups)
	items := block["itemListElement"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("inactive service should be skipped, got %d items", len(items))
	}
	if block["numberOfItems"] != 1 {
		t.Errorf("numberOfItems = %v, want 1", block["numberOfItems"])
	}
}

func TestFAQStructuredData(t *testing.T) {
	for _, lang := range []string{i18n.EN, i18n.ID} {
		block := faqStructuredData(lang)
		if got := block["@type"]; got != "FAQPage" {
			t.Errorf("%s: @type = %v, want FAQPage", lang, got)
		}
		questions, ok := block["mainEntity"].([]map[string]any)
		if !ok {
			t.Fatalf("%s: mainEntity is %T", lang, block["mainEntity"])
		}
		if len(questions) != 5 {
			t.Fatalf("%s: %d questions, want 5", lang, len(questions))
		}
		for i, q := range questions {
			n := i + 1
			if q["@type"] != "Question" {
				t.Errorf("%s q%d: @type = %v", lang, n, q["@type"])
			}
			wantQ := i18n.T(lang, fmt.Sprintf("faq.q%d", n))
			wantA := i18n.T(lang, fmt.Sprintf("faq.a%d", n))
			if q["name"] != wantQ {
				t.Errorf("%s q%d name = %q, want %q", lang, n, q["name"], wantQ)
			}
			if wantQ == fmt.Sprintf("faq.q%d", n) {
				t.Errorf("%s q%d is a missing dictionary key", lang, n)
			}
			ans, ok := q["acceptedAnswer"].(map[string]any)
			if !ok {
				t.Fatalf("%s q%d has no acceptedAnswer", lang, n)
			}
			if ans["@type"] != "Answer" {
				t.Errorf("%s q%d answer @type = %v", lang, n, ans["@type"])
			}
			if ans["text"] != wantA {
				t.Errorf("%s q%d answer = %q, want %q", lang, n, ans["text"], wantA)
			}
			if wantA == fmt.Sprintf("faq.a%d", n) {
				t.Errorf("%s q%d answer is a missing dictionary key", lang, n)
			}
		}
	}
	// A translated FAQ must not be the English one twice.
	if i18n.T(i18n.EN, "faq.q1") == i18n.T(i18n.ID, "faq.q1") {
		t.Error("faq.q1 is identical in English and Indonesian")
	}
}

func TestJSONLDHTML(t *testing.T) {
	out := string(jsonldHTML(
		siteStructuredData(seoTestBaseURL, "Setiap Hari 11:00–21:00 WIB", seoTestWhatsApp, sdLocations(), nil),
		faqStructuredData(i18n.ID),
	))
	const prefix = `<script type="application/ld+json">`
	const suffix = `</script>`
	if !strings.HasPrefix(out, prefix) {
		t.Fatalf("jsonldHTML must open with %s, got %q", prefix, out)
	}
	if !strings.HasSuffix(out, suffix) {
		t.Fatalf("jsonldHTML must close with %s", suffix)
	}
	payload := strings.TrimSuffix(strings.TrimPrefix(out, prefix), suffix)
	// Raw markup characters would break out of the script element.
	if strings.ContainsAny(payload, "<>&") {
		t.Errorf("payload contains unescaped markup characters: %s", payload)
	}
	var doc struct {
		Context string           `json:"@context"`
		Graph   []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal([]byte(payload), &doc); err != nil {
		t.Fatalf("payload is not valid JSON: %v\n%s", err, payload)
	}
	if doc.Context != "https://schema.org" {
		t.Errorf("@context = %q", doc.Context)
	}
	if len(doc.Graph) != 2 {
		t.Fatalf("@graph has %d nodes, want 2 (business + FAQ)", len(doc.Graph))
	}
	if doc.Graph[1]["@type"] != "FAQPage" {
		t.Errorf("second graph node = %v, want the FAQPage", doc.Graph[1]["@type"])
	}
}

// TestJSONLDHTMLFlattensAndEscapes pins the variadic contract (a slice of
// blocks flattens, a single map appends) and the injection guard: a hostile
// string is escaped by encoding/json, never emitted as live markup.
func TestJSONLDHTMLFlattensAndEscapes(t *testing.T) {
	slice := []map[string]any{{"@type": "A"}, {"@type": "B"}}
	hostile := map[string]any{"@type": "C", "name": `</script><img src=x onerror=alert(1)> & "quotes"`}
	out := string(jsonldHTML(slice, hostile))
	if strings.Count(out, "</script>") != 1 {
		t.Fatalf("hostile payload closed the script element early:\n%s", out)
	}
	if strings.Contains(out, "<img") {
		t.Fatalf("hostile payload escaped into markup:\n%s", out)
	}
	payload := strings.TrimSuffix(strings.TrimPrefix(out, `<script type="application/ld+json">`), "</script>")
	var doc struct {
		Graph []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal([]byte(payload), &doc); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	if len(doc.Graph) != 3 {
		t.Fatalf("@graph has %d nodes, want 3", len(doc.Graph))
	}
	// Round-trip: the escaped text survives as the original string.
	if got := doc.Graph[2]["name"]; got != hostile["name"] {
		t.Errorf("round-trip name = %q, want %q", got, hostile["name"])
	}
}

// TestSEOCopyIsReal guards the new dictionary entries: both languages carry
// plain sentences, not keys, and the FAQ answers stay short enough to render
// as a readable accordion.
func TestSEOCopyIsReal(t *testing.T) {
	for _, lang := range []string{i18n.EN, i18n.ID} {
		desc := i18n.T(lang, "seo.desc")
		if desc == "seo.desc" || strings.TrimSpace(desc) == "" {
			t.Errorf("%s: seo.desc is missing", lang)
		}
		if len(desc) > 200 {
			t.Errorf("%s: seo.desc is %d chars, too long for a meta description", lang, len(desc))
		}
		for n := 1; n <= 5; n++ {
			q := i18n.T(lang, fmt.Sprintf("faq.q%d", n))
			a := i18n.T(lang, fmt.Sprintf("faq.a%d", n))
			if q == fmt.Sprintf("faq.q%d", n) || a == fmt.Sprintf("faq.a%d", n) {
				t.Errorf("%s: faq %d is missing", lang, n)
				continue
			}
			if !strings.HasSuffix(q, "?") {
				t.Errorf("%s: faq.q%d should be a question, got %q", lang, n, q)
			}
			if len(a) < 40 || len(a) > 320 {
				t.Errorf("%s: faq.a%d length %d, want a 1-2 sentence answer", lang, n, len(a))
			}
		}
		if i18n.T(lang, "faq.title") == "faq.title" {
			t.Errorf("%s: faq.title is missing", lang)
		}
	}
}
