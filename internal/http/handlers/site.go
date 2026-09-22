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

// PortalSite lists hero banners for CS/admin management.
func (s *Server) PortalSite(w http.ResponseWriter, r *http.Request) {
	banners, _ := s.hero.ListAll(r.Context())
	s.render(w, r, "portal_site.html", pageData{
		"Banners": banners,
		"Error":   r.URL.Query().Get("error"),
	})
}

// PortalCreateBanner adds a hero banner (upload or link).
func (s *Server) PortalCreateBanner(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/portal/site?error="+url.QueryEscape("Could not read form"), http.StatusSeeOther)
			return
		}
	}
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	var photoPath string
	if file, header, ferr := r.FormFile("photo"); ferr == nil && header.Size > 0 {
		rel, msg := s.storeUpload(file, file, header.Filename, header.Size, "hero")
		if msg != "" {
			http.Redirect(w, r, "/portal/site?error="+url.QueryEscape(msg), http.StatusSeeOther)
			return
		}
		photoPath = rel
	}
	h := domain.HeroBanner{ImagePath: photoPath, ImageURL: strings.TrimSpace(r.FormValue("image_url"))}
	if err := s.hero.Save(r.Context(), &h); err != nil {
		http.Redirect(w, r, "/portal/site?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/portal/site?toast="+url.QueryEscape("Banner added")+"&kind=success", http.StatusSeeOther)
}

// PortalBannerSort reorders a banner.
func (s *Server) PortalBannerSort(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	if id, err := uuid.Parse(chi.URLParam(r, "id")); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(r.FormValue("sort"))); err == nil {
			_ = s.hero.SetSort(r.Context(), id, n)
		}
	}
	http.Redirect(w, r, "/portal/site", http.StatusSeeOther)
}

// PortalToggleBanner flips a banner's visibility.
func (s *Server) PortalToggleBanner(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	if id, err := uuid.Parse(chi.URLParam(r, "id")); err == nil {
		_ = s.hero.ToggleActive(r.Context(), id)
	}
	http.Redirect(w, r, "/portal/site?toast="+url.QueryEscape("Banner updated")+"&kind=success", http.StatusSeeOther)
}

// PortalDeleteBanner removes a banner permanently.
func (s *Server) PortalDeleteBanner(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	if id, err := uuid.Parse(chi.URLParam(r, "id")); err == nil {
		_ = s.hero.Delete(r.Context(), id)
	}
	http.Redirect(w, r, "/portal/site?toast="+url.QueryEscape("Banner removed")+"&kind=success", http.StatusSeeOther)
}

// ServeHeroPhoto serves uploaded banner photos (public, traversal-safe).
func (s *Server) ServeHeroPhoto(w http.ResponseWriter, r *http.Request) {
	s.serveUploadDir("hero")(w, r)
}
