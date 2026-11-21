package handlers

import (
	"context"
	"strings"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
)

// tvTestimonialCap is the same approved-testimonial budget the homepage
// filmstrip uses, so the kiosk and the site never disagree about what "the
// latest reviews" are. It is the *fetch* budget only — the television renders
// tvTestimonialSlideCap of them.
const tvTestimonialCap = 12

// The television is a fixed 1920x1080 frame with no scroll gesture, so the
// slide sets need hard render ceilings that CSS cannot give them honestly
// (overflow:hidden clips mid-glyph instead of removing a row). The production
// visual gate found a third review card clipped at the viewport bottom and a
// 13-item menu running past the fold; these caps are the measured ceilings,
// applied to the data the templates receive.
const (
	// tvTestimonialSlideCap is the two newest reviews: a third card clips at
	// TV type sizes on a 1080p panel.
	tvTestimonialSlideCap = 2
	// tvFoodSlideCap is ten menu rows: eleven-plus overflowed the frame.
	tvFoodSlideCap = 10
)

// tvSlideData is the kiosk's marketing side: catalog, approved testimonials,
// Bu Nah's menu and the review link. No booking ever enters this struct — the
// redaction contract lives in tvRow and this is the other half of the
// "marketing data only" ruling.
type tvSlideData struct {
	Services     []domain.Service
	Testimonials []domain.Testimonial
	Food         []domain.FoodItem
	ReviewURL    string
}

// tvFetchSlides is the raw slide-data seam (the tvQueueBookings precedent: the
// services hold concrete pgx repos, so tests swap the call). A read failure
// only drops that slide: the wall keeps the queue and the next 30s reload
// retries, because a kiosk has no staff to read an error page.
//
// The nil checks keep the render sweeps honest — they build a Server without
// services — while production always wires every service.
var tvFetchSlides = func(s *Server, ctx context.Context, loc uuid.UUID) tvSlideData {
	var out tvSlideData

	if s.catalog != nil {
		services, err := s.catalog.ListServices(ctx, loc, true)
		if err != nil {
			s.log.Error("tv slides services", "err", err)
		} else {
			out.Services = services
		}
	}

	if s.tm != nil {
		testimonials, err := s.tm.ListApproved(ctx, tvTestimonialCap)
		if err != nil {
			s.log.Error("tv slides testimonials", "err", err)
		} else {
			out.Testimonials = testimonials
		}
	}

	if s.food != nil {
		food, err := s.food.ListActive(ctx)
		if err != nil {
			s.log.Error("tv slides food", "err", err)
		} else {
			out.Food = food
		}
	}

	// The review slide reuses the WhatsApp settings' review_url (env fallback)
	// rather than adding a kiosk-only setting; review_enabled is deliberately
	// not consulted (it gates the post-visit message, not the QR card).
	if s.waSettings != nil {
		out.ReviewURL = strings.TrimSpace(s.waSettings.Effective(ctx).ReviewURL)
	}

	return out
}

// tvLoadSlides is the one entry point the kiosk handler calls: read the
// marketing sets, then enforce the 1080p render ceilings before any template
// sees them. The caps are server-side on purpose — a CSS clamp or overflow
// hides the overflow instead of dropping it, and the boundary row still ends
// mid-glyph on a fixed frame.
func tvLoadSlides(s *Server, ctx context.Context, loc uuid.UUID) tvSlideData {
	return capTVSlides(tvFetchSlides(s, ctx, loc))
}

// capTVSlides trims the slide sets to what a 1920x1080 television can actually
// show. It keeps the leading rows (ListApproved is newest-first, ListActive is
// sorted for display), so the deck shows "the latest" in both senses.
func capTVSlides(d tvSlideData) tvSlideData {
	if len(d.Testimonials) > tvTestimonialSlideCap {
		d.Testimonials = d.Testimonials[:tvTestimonialSlideCap]
	}
	if len(d.Food) > tvFoodSlideCap {
		d.Food = d.Food[:tvFoodSlideCap]
	}
	return d
}

// normalizeTVLayout maps ?layout= onto the two shells. Rotate is the default:
// it gives every slide the whole panel (the retired split layout collapsed Bu
// Nah's two-column menu grid into each other at 40% width). Only the exact
// literal "queue" opts into the market-free queue-only shell, so a malformed
// link can never leave a TV blank — and every legacy bookmark (?layout=split
// included) lands on rotate instead of an error.
func normalizeTVLayout(v string) string {
	if v == "queue" {
		return "queue"
	}
	return "rotate"
}
