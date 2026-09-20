package handlers

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/service"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// QuotePromo validates a code against the selected service price and renders
// the discount preview for the review step. Always answers 200 with an
// inline message (never a bare HTTP error) so Apply visibly responds; the
// booking creators re-validate on submit.
func (s *Server) QuotePromo(w http.ResponseWriter, r *http.Request) {
	fail := func(msg string) {
		s.renderPartial(w, r, "promo_preview.html", pageData{"Error": msg})
	}
	svcID, err := uuid.Parse(r.URL.Query().Get("service_id"))
	if err != nil {
		fail("Select a treatment first")
		return
	}
	svc, err := s.catalog.GetService(r.Context(), svcID)
	if err != nil {
		fail("Unknown treatment — pick it again")
		return
	}
	code := r.URL.Query().Get("code")
	if strings.TrimSpace(code) == "" {
		fail("Type a promo code first")
		return
	}
	q, err := s.promo.Quote(r.Context(), code, svc.PriceIDR)
	if err != nil {
		s.renderPartial(w, r, "promo_preview.html", pageData{"Error": err.Error()})
		return
	}
	s.renderPartial(w, r, "promo_preview.html", pageData{"Quote": q, "Price": svc.PriceIDR})
}

// PortalPromos lists every code for CS/admin management.
func (s *Server) PortalPromos(w http.ResponseWriter, r *http.Request) {
	codes, _ := s.promo.ListAll(r.Context())
	s.render(w, r, "portal_promos.html", pageData{
		"Promos": codes,
		"Error":  r.URL.Query().Get("error"),
	})
}

func parsePromoForm(r *http.Request) (domain.PromoCode, string) {
	var p domain.PromoCode
	if err := r.ParseForm(); err != nil {
		return p, "Could not read form"
	}
	p.Code = service.NormalizePromoCode(r.FormValue("code"))
	p.Kind = domain.PromoKind(strings.TrimSpace(r.FormValue("kind")))
	if v, err := strconv.Atoi(strings.TrimSpace(r.FormValue("value"))); err == nil {
		p.Value = v
	}
	if v := strings.TrimSpace(r.FormValue("min_spend")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			p.MinSpend = n
		}
	}
	if v := strings.TrimSpace(r.FormValue("max_uses")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			p.MaxUses = &n
		}
	}
	wib := time.FixedZone("WIB", 7*3600)
	if v := strings.TrimSpace(r.FormValue("valid_from")); v != "" {
		if t, err := time.ParseInLocation("2006-01-02", v, wib); err == nil {
			p.ValidFrom = &t
		}
	}
	if v := strings.TrimSpace(r.FormValue("valid_to")); v != "" {
		if t, err := time.ParseInLocation("2006-01-02", v, wib); err == nil {
			end := t.Add(24*time.Hour - time.Second)
			p.ValidTo = &end
		}
	}
	return p, ""
}

// PortalCreatePromo stores a new code (active immediately).
func (s *Server) PortalCreatePromo(w http.ResponseWriter, r *http.Request) {
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	p, errMsg := parsePromoForm(r)
	if errMsg != "" {
		http.Redirect(w, r, "/portal/promos?error="+url.QueryEscape(errMsg), http.StatusSeeOther)
		return
	}
	if err := s.promo.Save(r.Context(), &p); err != nil {
		http.Redirect(w, r, "/portal/promos?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/portal/promos?toast="+url.QueryEscape("Promo code added")+"&kind=success", http.StatusSeeOther)
}

// PortalTogglePromo flips a code's active flag.
func (s *Server) PortalTogglePromo(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	if id, err := uuid.Parse(chi.URLParam(r, "id")); err == nil {
		_ = s.promo.ToggleActive(r.Context(), id)
	}
	http.Redirect(w, r, "/portal/promos?toast="+url.QueryEscape("Promo code updated")+"&kind=success", http.StatusSeeOther)
}

// PortalDeletePromo removes a code (bookings keep their snapshots).
func (s *Server) PortalDeletePromo(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	if id, err := uuid.Parse(chi.URLParam(r, "id")); err == nil {
		_ = s.promo.Delete(r.Context(), id)
	}
	http.Redirect(w, r, "/portal/promos?toast="+url.QueryEscape("Promo code removed")+"&kind=success", http.StatusSeeOther)
}
