package handlers

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"spa-booking/internal/domain"
)

// tvOverflowFixtures builds the marketing deck with more rows than a 1080p
// television can show. The visual gate on production captured exactly this:
// a third review card clipped mid-glyph and a menu that ran past the fold, so
// the loader must cap both sets before the templates ever see them.
func tvOverflowFixtures(testimonials, food int) tvSlideData {
	d := tvSlideFixtures()

	d.Testimonials = make([]domain.Testimonial, 0, testimonials)
	for i := 0; i < testimonials; i++ {
		d.Testimonials = append(d.Testimonials, domain.Testimonial{
			Name:    fmt.Sprintf("Pengulas %02d", i+1),
			Rating:  5,
			Message: fmt.Sprintf("Ulasan nomor %02d yang cukup panjang.", i+1),
			Status:  domain.TestimonialApproved,
		})
	}

	priceM := int64(20000)
	d.Food = make([]domain.FoodItem, 0, food)
	for i := 0; i < food; i++ {
		d.Food = append(d.Food, domain.FoodItem{
			Name:     fmt.Sprintf("Menu %02d", i+1),
			PriceM:   &priceM,
			Category: domain.FoodCategoryFood,
			Active:   true,
		})
	}

	return d
}

// The testimonial slide shows the two newest reviews. The loader still fetches
// the shared 12-deep budget so the kiosk and the homepage filmstrip agree on
// which reviews are "latest", but at TV type sizes a third card clips at the
// viewport bottom — so exactly two render.
func TestTVTestimonialSlideCapsAtTwo(t *testing.T) {
	h := newRouteTestServer(t)
	tok := tvFixture()
	swapTVActive(t, &tok, nil, nil)
	swapTVClock(t, tvQueueNow)
	swapTVQueue(t, nil, nil, nil)
	swapTVSlides(t, tvOverflowFixtures(6, 1))

	rec := runRoute(h, httptest.NewRequest(http.MethodGet, "/tv?lang=id&t="+tvTestToken, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	out := rec.Body.String()

	if got := strings.Count(out, `<li class="tv-tm">`); got != 2 {
		t.Errorf("rendered testimonial cards = %d, want 2", got)
	}
	for _, want := range []string{"Pengulas 01", "Pengulas 02"} {
		if !contains(out, want) {
			t.Errorf("missing kept testimonial %q", want)
		}
	}
	if contains(out, "Pengulas 03") {
		t.Error("third testimonial rendered past the 1080p ceiling")
	}
}

// Bu Nah's menu renders at most ten rows: thirteen-plus items overflowed the
// 1080p frame in the production capture, and the slide has no scroll gesture.
func TestTVFoodSlideCapsAtTen(t *testing.T) {
	h := newRouteTestServer(t)
	tok := tvFixture()
	swapTVActive(t, &tok, nil, nil)
	swapTVClock(t, tvQueueNow)
	swapTVQueue(t, nil, nil, nil)
	swapTVSlides(t, tvOverflowFixtures(1, 14))

	rec := runRoute(h, httptest.NewRequest(http.MethodGet, "/tv?lang=id&t="+tvTestToken, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	out := rec.Body.String()

	if got := strings.Count(out, `<li class="tv-food">`); got != 10 {
		t.Errorf("rendered menu rows = %d, want 10", got)
	}
	for _, want := range []string{"Menu 01", "Menu 10"} {
		if !contains(out, want) {
			t.Errorf("missing kept menu item %q", want)
		}
	}
	if contains(out, "Menu 11") {
		t.Error("eleventh menu row rendered past the 1080p ceiling")
	}
}
