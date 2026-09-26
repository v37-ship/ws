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

// GalleryPage renders the public service gallery (photos + linked videos).
func (s *Server) GalleryPage(w http.ResponseWriter, r *http.Request) {
	items, err := s.gallery.ListActive(r.Context())
	if err != nil {
		http.Error(w, "failed to load gallery", http.StatusInternalServerError)
		return
	}
	// Distinct linked services, in first-seen order, for the filter chips.
	var services []domain.Service
	seen := map[uuid.UUID]bool{}
	for _, g := range items {
		if g.ServiceID != nil && !seen[*g.ServiceID] {
			seen[*g.ServiceID] = true
			services = append(services, domain.Service{ID: *g.ServiceID, Name: g.ServiceName})
		}
	}
	s.render(w, r, "gallery.html", pageData{
		"Items":    items,
		"Services": services,
	})
}

// galleryServices collects active services across locations for the
// manager's link picker, labeled "Service — Location".
func (s *Server) galleryServices(r *http.Request) []domain.Service {
	var out []domain.Service
	locs, err := s.catalog.ListLocations(r.Context(), true)
	if err != nil {
		return nil
	}
	for _, l := range locs {
		svcs, err := s.catalog.ListServices(r.Context(), l.ID, true)
		if err != nil {
			continue
		}
		for _, svc := range svcs {
			svc.Name = svc.Name + " — " + l.Name
			out = append(out, svc)
		}
	}
	return out
}

func parseGalleryForm(r *http.Request) (domain.GalleryItem, string) {
	var g domain.GalleryItem
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		if err := r.ParseForm(); err != nil {
			return g, "Could not read form"
		}
	}
	g.Kind = domain.GalleryKind(strings.TrimSpace(r.FormValue("kind")))
	g.Title = r.FormValue("title")
	g.Caption = r.FormValue("caption")
	if v := strings.TrimSpace(r.FormValue("service_id")); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			g.ServiceID = &id
		} else {
			return g, "Invalid service"
		}
	}
	g.VideoURL = strings.TrimSpace(r.FormValue("video_url"))
	g.ImageURL = strings.TrimSpace(r.FormValue("image_url"))
	g.ThumbURL = strings.TrimSpace(r.FormValue("thumb_url"))
	if v := strings.TrimSpace(r.FormValue("sort")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			g.Sort = n
		}
	}
	return g, ""
}

// PortalGallery lists every gallery row for CS/admin management.
func (s *Server) PortalGallery(w http.ResponseWriter, r *http.Request) {
	items, _ := s.gallery.ListAll(r.Context())
	svcs := s.galleryServices(r)
	s.render(w, r, "portal_gallery.html", pageData{
		"Items":    items,
		"Services": svcs,
		"Error":    r.URL.Query().Get("error"),
	})
}

// PortalCreateGallery adds a gallery row (photo upload/link or video link).
func (s *Server) PortalCreateGallery(w http.ResponseWriter, r *http.Request) {
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	g, errMsg := parseGalleryForm(r)
	if errMsg != "" {
		http.Redirect(w, r, "/portal/gallery?error="+url.QueryEscape(errMsg), http.StatusSeeOther)
		return
	}
	if file, header, ferr := r.FormFile("photo"); ferr == nil && header.Size > 0 {
		rel, msg := s.storeUpload(file, file, header.Filename, header.Size, "gallery")
		if msg != "" {
			http.Redirect(w, r, "/portal/gallery?error="+url.QueryEscape(msg), http.StatusSeeOther)
			return
		}
		g.ImagePath = rel
	}
	if file, header, ferr := r.FormFile("thumb"); ferr == nil && header.Size > 0 {
		rel, msg := s.storeUpload(file, file, header.Filename, header.Size, "gallery")
		if msg != "" {
			http.Redirect(w, r, "/portal/gallery?error="+url.QueryEscape(msg), http.StatusSeeOther)
			return
		}
		g.ThumbPath = rel
	}
	if err := s.gallery.Save(r.Context(), &g); err != nil {
		http.Redirect(w, r, "/portal/gallery?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/portal/gallery?toast="+url.QueryEscape("Gallery item added")+"&kind=success", http.StatusSeeOther)
}

// PortalEditGalleryForm renders the gallery-row editor.
func (s *Server) PortalEditGalleryForm(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	g, err := s.gallery.ByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, r, "portal_gallery_edit.html", pageData{
		"Item":     g,
		"Services": s.galleryServices(r),
		"Error":    r.URL.Query().Get("error"),
	})
}

// PortalUpdateGallery saves gallery-row edits.
func (s *Server) PortalUpdateGallery(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	existing, err := s.gallery.ByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	g, errMsg := parseGalleryForm(r)
	if errMsg != "" {
		http.Redirect(w, r, "/portal/gallery/"+id.String()+"/edit?error="+url.QueryEscape(errMsg), http.StatusSeeOther)
		return
	}
	g.ID = id
	g.Active = existing.Active
	g.ImagePath = existing.ImagePath
	g.ThumbPath = existing.ThumbPath
	if file, header, ferr := r.FormFile("photo"); ferr == nil && header.Size > 0 {
		rel, msg := s.storeUpload(file, file, header.Filename, header.Size, "gallery")
		if msg != "" {
			http.Redirect(w, r, "/portal/gallery/"+id.String()+"/edit?error="+url.QueryEscape(msg), http.StatusSeeOther)
			return
		}
		g.ImagePath = rel
	}
	if file, header, ferr := r.FormFile("thumb"); ferr == nil && header.Size > 0 {
		rel, msg := s.storeUpload(file, file, header.Filename, header.Size, "gallery")
		if msg != "" {
			http.Redirect(w, r, "/portal/gallery/"+id.String()+"/edit?error="+url.QueryEscape(msg), http.StatusSeeOther)
			return
		}
		g.ThumbPath = rel
	}
	if err := s.gallery.Save(r.Context(), &g); err != nil {
		http.Redirect(w, r, "/portal/gallery/"+id.String()+"/edit?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/portal/gallery?toast="+url.QueryEscape("Gallery item updated")+"&kind=success", http.StatusSeeOther)
}

// PortalToggleGallery flips a row's visibility on the public page.
func (s *Server) PortalToggleGallery(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	if id, err := uuid.Parse(chi.URLParam(r, "id")); err == nil {
		_ = s.gallery.ToggleActive(r.Context(), id)
	}
	http.Redirect(w, r, "/portal/gallery?toast="+url.QueryEscape("Gallery updated")+"&kind=success", http.StatusSeeOther)
}

// PortalDeleteGallery removes a gallery row permanently.
func (s *Server) PortalDeleteGallery(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	if id, err := uuid.Parse(chi.URLParam(r, "id")); err == nil {
		_ = s.gallery.Delete(r.Context(), id)
	}
	http.Redirect(w, r, "/portal/gallery?toast="+url.QueryEscape("Gallery item removed")+"&kind=success", http.StatusSeeOther)
}

// PortalGallerySort reorders a gallery row.
func (s *Server) PortalGallerySort(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	if id, err := uuid.Parse(chi.URLParam(r, "id")); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(r.FormValue("sort"))); err == nil {
			_ = s.gallery.SetSort(r.Context(), id, n)
		}
	}
	http.Redirect(w, r, "/portal/gallery", http.StatusSeeOther)
}

// ServeGalleryPhoto serves uploaded gallery photos (public, traversal-safe).
func (s *Server) ServeGalleryPhoto(w http.ResponseWriter, r *http.Request) {
	s.serveUploadDir("gallery")(w, r)
}
