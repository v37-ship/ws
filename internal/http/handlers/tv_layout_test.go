package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
)

// tvReviewTestURL is the effective review link the kiosk reads from the
// WhatsApp settings service (wa_settings.review_url, env fallback) — the TV
// adds no configuration of its own (ruling #12).
const tvReviewTestURL = "https://g.page/r/CTvExample/review"

// swapTVSlides pins the kiosk's marketing data seam. Like tvQueueBookings, the
// catalog/testimonial/food services are concrete types, so the tests swap the
// whole loader instead of opening a database.
func swapTVSlides(t *testing.T, slides tvSlideData) {
	t.Helper()
	prev := tvLoadSlides
	tvLoadSlides = func(_ *Server, _ context.Context, _ uuid.UUID) tvSlideData { return slides }
	t.Cleanup(func() { tvLoadSlides = prev })
}

// tvSlideFixtures is marketing data only: active catalog rows, an approved
// testimonial, a Bu Nah menu item and the review link. Prices deliberately
// differ from tvHostileBooking's Rp 350.000 so the price-leak assertion in the
// no-PII test cannot mistake a legitimate service price for booking data.
func tvSlideFixtures() tvSlideData {
	priceM, priceL := int64(25000), int64(35000)
	return tvSlideData{
		Services: []domain.Service{
			{Name: "Pijat Balinese", NameID: "Pijat Bali", PriceIDR: 275000, DurationMin: 90, Active: true},
			{Name: "Baby Massage", PriceIDR: 210000, DurationMin: 45, Active: true},
		},
		Testimonials: []domain.Testimonial{
			{Name: "Rina Kartika", Rating: 5, Message: "Enak sekali, bayi saya tenang.", Status: domain.TestimonialApproved},
		},
		Food: []domain.FoodItem{
			{Name: "Nasgor Bu Nah", PriceM: &priceM, PriceL: &priceL, Category: domain.FoodCategoryFood},
		},
		ReviewURL: tvReviewTestURL,
	}
}

// The layout param is pure and pinned: only the exact literal "rotate" leaves
// the split default. A stray space or casing must not switch the shell.
func TestNormalizeTVLayout(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "split"},
		{"split", "split"},
		{"rotate", "rotate"},
		{"ROTATE", "split"},
		{" rotate", "split"},
		{"rotate ", "split"},
		{"fullscreen", "split"},
	}
	for _, c := range cases {
		if got := normalizeTVLayout(c.in); got != c.want {
			t.Errorf("normalizeTVLayout(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The default shell: queue + bands pinned on the left, the marketing deck on
// the right, and the 30s poll unchanged. The queue must not sit inside the
// deck (that is the rotate layout's shape).
func TestTVSplitLayoutShowsQueueAndSlides(t *testing.T) {
	h := newRouteTestServer(t)
	tok := tvFixture()
	swapTVActive(t, &tok, nil, nil)
	swapTVClock(t, tvQueueNow)
	swapTVQueue(t, []domain.Booking{
		tvQueueBooking("Sari Wijaya", "Pijat Balinese", "Ayu Lestari", domain.BookingInService, domain.PlaceOnsite, 10, 0),
	}, nil, nil)
	swapTVSlides(t, tvSlideFixtures())

	rec := runRoute(h, httptest.NewRequest(http.MethodGet, "/tv?lang=id&t="+tvTestToken, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	out := rec.Body.String()

	for _, want := range []string{
		"tv-shell-split",
		`id="tv-live"`,
		`hx-trigger="every 30s"`,
		`x-data="tvDeck(15000)"`,
		"Sedang dilayani",
		"Sari W.",
		"tv-deck-split",
		"Perawatan &amp; harga",
		"Pijat Bali",
		"Rp 275.000",
		"Nasgor Bu Nah",
		"Rina Kartika",
		"Enak sekali, bayi saya tenang.",
		`id="tv-qr"`,
		tvReviewTestURL,
		"js/qrcode.min.js",
	} {
		if !contains(out, want) {
			t.Errorf("split layout missing %q", want)
		}
	}
	if contains(out, "tv-deck-rotate") || contains(out, "tv-slide-queue") {
		t.Error("split layout must pin the queue outside the deck")
	}
	if got := strings.Count(out, `id="tv-live"`); got != 1 {
		t.Errorf("queue poll wrapper count = %d, want 1", got)
	}
}

// The rotate shell: one full-screen slide at a time, with the queue as a slide
// in the same deck, the same slide partials, and the same poll wrapper.
func TestTVRotateLayoutShowsRotationDeck(t *testing.T) {
	h := newRouteTestServer(t)
	tok := tvFixture()
	swapTVActive(t, &tok, nil, nil)
	swapTVClock(t, tvQueueNow)
	swapTVQueue(t, []domain.Booking{
		tvQueueBooking("Sari Wijaya", "Pijat Balinese", "Ayu Lestari", domain.BookingInService, domain.PlaceOnsite, 10, 0),
	}, nil, nil)
	swapTVSlides(t, tvSlideFixtures())

	rec := runRoute(h, httptest.NewRequest(http.MethodGet, "/tv?lang=id&t="+tvTestToken+"&layout=rotate", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	out := rec.Body.String()

	for _, want := range []string{
		"tv-shell-rotate",
		"tv-deck-rotate",
		"tv-slide-queue",
		`id="tv-live"`,
		`hx-trigger="every 30s"`,
		`x-data="tvDeck(15000)"`,
		"Sari W.",
		"Perawatan &amp; harga",
		"Pijat Bali",
		"Nasgor Bu Nah",
		"Rina Kartika",
		`id="tv-qr"`,
		tvReviewTestURL,
		"js/qrcode.min.js",
	} {
		if !contains(out, want) {
			t.Errorf("rotate layout missing %q", want)
		}
	}
	if contains(out, "tv-shell-split") || contains(out, "tv-deck-split") {
		t.Error("rotate layout must not render the split shell")
	}
	if got := strings.Count(out, `id="tv-live"`); got != 1 {
		t.Errorf("queue poll wrapper count = %d, want 1", got)
	}
}

// The review slide is conditional on the settings URL and nothing else:
// review_enabled is not consulted, and an empty URL omits the slide (and the
// QR library) entirely.
func TestTVReviewSlideOnlyWhenURLSet(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want bool
	}{
		{"url set", tvReviewTestURL, true},
		{"url empty", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newRouteTestServer(t)
			tok := tvFixture()
			swapTVActive(t, &tok, nil, nil)
			swapTVClock(t, tvQueueNow)
			swapTVQueue(t, nil, nil, nil)
			slides := tvSlideFixtures()
			slides.ReviewURL = c.url
			swapTVSlides(t, slides)

			rec := runRoute(h, httptest.NewRequest(http.MethodGet, "/tv?lang=id&t="+tvTestToken, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			out := rec.Body.String()

			if got := contains(out, `id="tv-qr"`); got != c.want {
				t.Errorf("QR element present = %v, want %v", got, c.want)
			}
			if got := contains(out, "js/qrcode.min.js"); got != c.want {
				t.Errorf("QR library included = %v, want %v", got, c.want)
			}
			if got := contains(out, "Suka kunjungan Anda?"); got != c.want {
				t.Errorf("review slide heading present = %v, want %v", got, c.want)
			}
		})
	}
}

// Re-run Task 2's hostile assertion across BOTH shells and every new slide:
// the page renders queue/marketing data only, never booking PII.
func TestTVNoPIIAcrossLayoutsAndSlides(t *testing.T) {
	h := newRouteTestServer(t)
	tok := tvFixture()
	swapTVActive(t, &tok, nil, nil)
	swapTVClock(t, tvQueueNow)
	swapTVQueue(t, []domain.Booking{tvHostileBooking()}, nil, nil)
	swapTVSlides(t, tvSlideFixtures())

	for _, suffix := range []string{"", "&layout=rotate"} {
		path := "/tv?lang=id&t=" + tvTestToken + suffix
		rec := runRoute(h, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", path, rec.Code)
		}
		out := rec.Body.String()
		for _, want := range []string{"Sari W.", "Pijat Balinese", "Perawatan &amp; harga", "Nasgor Bu Nah"} {
			if !contains(out, want) {
				t.Errorf("%s: missing %q", path, want)
			}
		}
		low := strings.ToLower(out)
		for _, leak := range []string{
			"sari wijaya",
			"081234567890",
			"sari@example.com",
			"bayi sensitif",
			"asi eksklusif",
			"alergi lanolin",
			"jl. rahasia",
			"pending",
			"qris",
			"350.000",
			"lestari",
		} {
			if strings.Contains(low, leak) {
				t.Errorf("%s: leaked %q into the kiosk HTML", path, leak)
			}
		}
	}
}
