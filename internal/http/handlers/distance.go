package handlers

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"spa-booking/internal/domain"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// parseZoneID reads an optional distance-band form value.
func parseZoneID(raw string) *uuid.UUID {
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil
	}
	id, err := uuid.Parse(v)
	if err != nil || id == uuid.Nil {
		return nil
	}
	return &id
}

// mapsOriginString renders a branch address as a Maps origin.
func mapsOriginString(address, city string) string {
	parts := []string{strings.TrimSpace(address), strings.TrimSpace(city), "Indonesia"}
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, ", ")
}

// PortalDistance is the transport fee manager (bands → fee).
func (s *Server) PortalDistance(w http.ResponseWriter, r *http.Request) {
	zones, _ := s.distance.ListAll(r.Context())
	s.render(w, r, "portal_distance.html", pageData{
		"Zones": zones,
		"Error": r.URL.Query().Get("error"),
	})
}

func parseZoneForm(r *http.Request) (domain.DistanceZone, string) {
	var z domain.DistanceZone
	if err := r.ParseForm(); err != nil {
		return z, "Could not read form"
	}
	parseKm := func(name string) (float64, bool) {
		v := strings.TrimSpace(r.FormValue(name))
		if v == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(strings.ReplaceAll(v, ",", "."), 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	minKM, ok := parseKm("min_km")
	if !ok {
		return z, "Enter the minimum distance in km"
	}
	maxKM, ok := parseKm("max_km")
	if !ok {
		return z, "Enter the maximum distance in km"
	}
	z.MinKM = minKM
	z.MaxKM = maxKM
	if v := strings.TrimSpace(r.FormValue("fee_idr")); v != "" {
		n, err := strconv.ParseInt(strings.ReplaceAll(v, ".", ""), 10, 64)
		if err != nil {
			return z, "Enter the fee in rupiah"
		}
		z.FeeIDR = n
	}
	z.Active = r.FormValue("active") == "on" || r.FormValue("active") == "true"
	if v, err := strconv.Atoi(strings.TrimSpace(r.FormValue("sort"))); err == nil {
		z.Sort = v
	}
	return z, ""
}

// PortalCreateDistance adds a band.
func (s *Server) PortalCreateDistance(w http.ResponseWriter, r *http.Request) {
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	z, errMsg := parseZoneForm(r)
	if errMsg == "" {
		if err := s.distance.Save(r.Context(), &z); err != nil {
			errMsg = err.Error()
		}
	}
	if errMsg != "" {
		http.Redirect(w, r, "/portal/distance?error="+url.QueryEscape(errMsg), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/portal/distance?toast="+url.QueryEscape("Distance band added")+"&kind=success", http.StatusSeeOther)
}

// PortalUpdateDistance edits a band. Bookings already hold a fee snapshot,
// so past money never moves.
func (s *Server) PortalUpdateDistance(w http.ResponseWriter, r *http.Request) {
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	z, errMsg := parseZoneForm(r)
	if errMsg == "" {
		z.ID = id
		if err := s.distance.Save(r.Context(), &z); err != nil {
			errMsg = err.Error()
		}
	}
	if errMsg != "" {
		http.Redirect(w, r, "/portal/distance?error="+url.QueryEscape(errMsg), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/portal/distance?toast="+url.QueryEscape("Distance band updated")+"&kind=success", http.StatusSeeOther)
}

// PortalToggleDistance flips a band's active flag (disabled bands stop
// being offered but stay editable).
func (s *Server) PortalToggleDistance(w http.ResponseWriter, r *http.Request) {
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.distance.ToggleActive(r.Context(), id); err != nil {
		http.Redirect(w, r, "/portal/distance?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/portal/distance", http.StatusSeeOther)
}

// PortalDeleteDistance removes a band (bookings keep their snapshot).
func (s *Server) PortalDeleteDistance(w http.ResponseWriter, r *http.Request) {
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.distance.Delete(r.Context(), id); err != nil {
		http.Redirect(w, r, "/portal/distance?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/portal/distance?toast="+url.QueryEscape("Distance band deleted")+"&kind=success", http.StatusSeeOther)
}
