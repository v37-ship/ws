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

// The layout param is pure and pinned. Rotate is the default, so everything
// that is not the exact literal "queue" — missing, casing, junk, and the
// retired "split" — lands there: a legacy split bookmark still opens a working
// TV instead of an error or a deleted shell.
func TestNormalizeTVLayout(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "rotate"},
		{"rotate", "rotate"},
		{"ROTATE", "rotate"},
		{" rotate", "rotate"},
		{"rotate ", "rotate"},
		{"split", "rotate"},
		{"SPLIT", "rotate"},
		{"fullscreen", "rotate"},
		{"queue", "queue"},
		{"QUEUE", "rotate"},
		{" queue", "rotate"},
		{"queue ", "rotate"},
	}
	for _, c := range cases {
		if got := normalizeTVLayout(c.in); got != c.want {
			t.Errorf("normalizeTVLayout(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Rotate is the default shell: one full-screen slide at a time, the queue as
// one slide in the same deck, every slide partial and the same 30s poll. The
// legacy split param (and any junk) must render exactly this, never a deleted
// split shell.
func TestTVDefaultLayoutIsTheRotateDeck(t *testing.T) {
	for _, tc := range []struct{ name, query string }{
		{"no layout param", ""},
		{"explicit rotate", "&layout=rotate"},
		{"legacy split bookmark", "&layout=split"},
		{"junk layout", "&layout=fullscreen"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newRouteTestServer(t)
			tok := tvFixture()
			swapTVActive(t, &tok, nil, nil)
			swapTVClock(t, tvQueueNow)
			swapTVQueue(t, []domain.Booking{
				tvQueueBooking("Sari Wijaya", "Pijat Balinese", "Ayu Lestari", domain.BookingInService, domain.PlaceOnsite, 10, 0),
			}, nil, nil)
			swapTVSlides(t, tvSlideFixtures())

			rec := runRoute(h, httptest.NewRequest(http.MethodGet, "/tv?lang=id&t="+tvTestToken+tc.query, nil))
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
				"Sedang dilayani",
				"Sari W.",
				"Perawatan &amp; harga",
				"Pijat Bali",
				"Rp 275.000",
				"Nasgor Bu Nah",
				"Rina Kartika",
				"Enak sekali, bayi saya tenang.",
				`id="tv-qr"`,
				tvReviewTestURL,
				"js/qrcode.min.js",
				"12 * 60 * 60 * 1000", // the daily-ish reload timer
			} {
				if !contains(out, want) {
					t.Errorf("rotate layout missing %q", want)
				}
			}
			for _, unwanted := range []string{"tv-shell-split", "tv-deck-split", "tv-split", "tv-shell-queue"} {
				if contains(out, unwanted) {
					t.Errorf("rotate layout must not render %q", unwanted)
				}
			}
			if got := strings.Count(out, `id="tv-live"`); got != 1 {
				t.Errorf("queue poll wrapper count = %d, want 1", got)
			}
		})
	}
}

// Queue-only is the market-free shell: the bands and the day list at full
// width, the same 30s poll, the same daily reload — and not one byte of the
// slide set. The handler does not even load the marketing data.
func TestTVQueueOnlyLayoutRendersNoMarketing(t *testing.T) {
	h := newRouteTestServer(t)
	tok := tvFixture()
	swapTVActive(t, &tok, nil, nil)
	swapTVClock(t, tvQueueNow)
	swapTVQueue(t, []domain.Booking{
		tvQueueBooking("Sari Wijaya", "Pijat Balinese", "Ayu Lestari", domain.BookingInService, domain.PlaceOnsite, 10, 0),
	}, nil, nil)

	slideCalls := 0
	prev := tvLoadSlides
	tvLoadSlides = func(_ *Server, _ context.Context, _ uuid.UUID) tvSlideData {
		slideCalls++
		return tvSlideFixtures()
	}
	t.Cleanup(func() { tvLoadSlides = prev })

	rec := runRoute(h, httptest.NewRequest(http.MethodGet, "/tv?lang=id&t="+tvTestToken+"&layout=queue", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	out := rec.Body.String()

	for _, want := range []string{
		"tv-shell-queue",
		`id="tv-live"`,
		`hx-trigger="every 30s"`,
		"Sedang dilayani",
		"Berikutnya",
		"Jadwal hari ini",
		"Sari W.",
		"Pijat Balinese",
		"12 * 60 * 60 * 1000", // the daily-ish reload timer
	} {
		if !contains(out, want) {
			t.Errorf("queue layout missing %q", want)
		}
	}
	// The whole marketing half is gone, not merely hidden: no slide markup,
	// no deck component, no slides region and no QR renderer.
	for _, unwanted := range []string{
		"tv-slide", "tv-deck", "tvDeck", "tv-slides",
		"qrcode.min.js", `id="tv-qr"`,
		"Perawatan &amp; harga", "Rp 275.000",
		"Nasgor Bu Nah", "Rina Kartika", "Suka kunjungan Anda?",
		tvReviewTestURL,
		"tv-shell-rotate", "tv-shell-split",
	} {
		if contains(out, unwanted) {
			t.Errorf("queue layout must not render %q", unwanted)
		}
	}
	if got := strings.Count(out, `id="tv-live"`); got != 1 {
		t.Errorf("queue poll wrapper count = %d, want 1", got)
	}
	if slideCalls != 0 {
		t.Errorf("queue layout loaded the marketing slides %d times, want 0", slideCalls)
	}

	// The queue shell is translated too (the i18n sweep only renders the
	// default shell).
	en := runRoute(h, httptest.NewRequest(http.MethodGet, "/tv?lang=en&t="+tvTestToken+"&layout=queue", nil))
	if body := en.Body.String(); !contains(body, "Waiting-room queue") || !contains(body, "Up next") {
		t.Errorf("queue layout (en) is missing its translated copy")
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

// Re-run the hostile assertion across BOTH shells and every slide: the page
// renders queue/marketing data only, never booking PII. Marketing is expected
// in rotate and must be absent in queue-only.
func TestTVNoPIIAcrossLayoutsAndSlides(t *testing.T) {
	h := newRouteTestServer(t)
	tok := tvFixture()
	swapTVActive(t, &tok, nil, nil)
	swapTVClock(t, tvQueueNow)
	swapTVQueue(t, []domain.Booking{tvHostileBooking()}, nil, nil)
	swapTVSlides(t, tvSlideFixtures())

	for _, tc := range []struct {
		name      string
		query     string
		marketing bool
	}{
		{"queue only", "&layout=queue", false},
		{"rotate", "&layout=rotate", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := "/tv?lang=id&t=" + tvTestToken + tc.query
			rec := runRoute(h, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("%s: status = %d, want 200", path, rec.Code)
			}
			out := rec.Body.String()
			// The queue is on both shells; marketing only on rotate.
			for _, want := range []string{"Sari W.", "Pijat Balinese"} {
				if !contains(out, want) {
					t.Errorf("%s: missing %q", path, want)
				}
			}
			for _, want := range []string{"Perawatan &amp; harga", "Nasgor Bu Nah", "Rina Kartika", `id="tv-qr"`} {
				if got := contains(out, want); got != tc.marketing {
					t.Errorf("%s: marketing marker %q present = %v, want %v", path, want, got, tc.marketing)
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
		})
	}
}
