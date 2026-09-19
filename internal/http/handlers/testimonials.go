package handlers

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"spa-booking/internal/domain"
	"spa-booking/internal/http/middleware"
	"spa-booking/internal/service"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// TestimonialNewForm renders the customer review form (login required).
func (s *Server) TestimonialNewForm(w http.ResponseWriter, r *http.Request) {
	u := middleware.UserFrom(r.Context())
	name, avatar := "", ""
	if u != nil {
		name = u.FullName
		avatar = service.ResolveAvatar(*u)
	}
	s.render(w, r, "testimonial_new.html", pageData{
		"Name": name, "Avatar": avatar,
		"Error": r.URL.Query().Get("error"),
	})
}

// snapshotPhoto resolves the review photo: fresh upload wins, then an
// explicit link, then the author's current account avatar (uploaded avatars
// are copied so later account edits never rewrite published reviews).
func (s *Server) snapshotPhoto(r *http.Request, u *domain.User) (string, string, string) {
	if file, header, ferr := r.FormFile("photo"); ferr == nil && header.Size > 0 {
		rel, msg := s.storeUpload(file, file, header.Filename, header.Size, "testimonials")
		if msg != "" {
			return "", "", msg
		}
		return rel, "", ""
	}
	if link := strings.TrimSpace(r.FormValue("photo_url")); link != "" {
		if err := service.ValidateImageURL(link); err != nil {
			return "", "", err.Error()
		}
		return "", link, ""
	}
	if u != nil && u.PhotoPath != "" {
		src, err := os.Open(filepath.Join(s.cfg.UploadDir, filepath.Clean(u.PhotoPath)))
		if err != nil {
			return "", "", ""
		}
		defer src.Close()
		info, err := src.Stat()
		if err != nil {
			return "", "", ""
		}
		rel, msg := s.storeUpload(src, src, filepath.Base(u.PhotoPath), info.Size(), "testimonials")
		if msg != "" {
			return "", "", ""
		}
		return rel, "", ""
	}
	if u != nil {
		return "", strings.TrimSpace(u.PhotoURL), ""
	}
	return "", "", ""
}

// CreateTestimonial saves a customer review as pending moderation.
func (s *Server) CreateTestimonial(w http.ResponseWriter, r *http.Request) {
	u := middleware.UserFrom(r.Context())
	if u == nil {
		http.Redirect(w, r, "/login?next=/testimonials/new", http.StatusSeeOther)
		return
	}
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/testimonials/new?error="+url.QueryEscape("Could not read form"), http.StatusSeeOther)
			return
		}
	}
	if !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		name = u.FullName
	}
	rating, _ := strconv.Atoi(r.FormValue("rating"))
	age, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("age")))
	photoPath, photoURL, msg := s.snapshotPhoto(r, u)
	if msg != "" {
		http.Redirect(w, r, "/testimonials/new?error="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}
	t := &domain.Testimonial{
		CustomerID: &u.ID, Name: name, Age: age,
		PhotoPath: photoPath, PhotoURL: photoURL,
		Rating: rating, Message: r.FormValue("message"),
	}
	if err := s.tm.Submit(r.Context(), t); err != nil {
		http.Redirect(w, r, "/testimonials/new?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/?toast="+url.QueryEscape("Thank you! Your review is awaiting moderation.")+"&kind=success#testimonials", http.StatusSeeOther)
}

// AccountForm renders the self-service profile page (login required).
func (s *Server) AccountForm(w http.ResponseWriter, r *http.Request) {
	u := middleware.UserFrom(r.Context())
	avatar := ""
	if u != nil {
		avatar = service.ResolveAvatar(*u)
	}
	s.render(w, r, "account.html", pageData{
		"Avatar": avatar,
		"Error":  r.URL.Query().Get("error"),
	})
}

// UpdateAccount saves the display name and profile photo (upload or link).
func (s *Server) UpdateAccount(w http.ResponseWriter, r *http.Request) {
	u := middleware.UserFrom(r.Context())
	if u == nil {
		http.Redirect(w, r, "/login?next=/account", http.StatusSeeOther)
		return
	}
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/account?error="+url.QueryEscape("Could not read form"), http.StatusSeeOther)
			return
		}
	}
	if !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	photoPath, photoURL := u.PhotoPath, u.PhotoURL
	if file, header, ferr := r.FormFile("photo"); ferr == nil && header.Size > 0 {
		rel, msg := s.storeUpload(file, file, header.Filename, header.Size, "avatars")
		if msg != "" {
			http.Redirect(w, r, "/account?error="+url.QueryEscape(msg), http.StatusSeeOther)
			return
		}
		photoPath, photoURL = rel, ""
	} else if link := strings.TrimSpace(r.FormValue("photo_url")); link != "" {
		if err := service.ValidateImageURL(link); err != nil {
			http.Redirect(w, r, "/account?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
			return
		}
		photoPath, photoURL = "", link
	}
	if _, err := s.auth.UpdateProfile(r.Context(), u.ID, r.FormValue("full_name"), photoPath, photoURL); err != nil {
		http.Redirect(w, r, "/account?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/account?toast="+url.QueryEscape("Profile updated")+"&kind=success", http.StatusSeeOther)
}

// AdminTestimonials lists every testimonial for moderation.
func (s *Server) AdminTestimonials(w http.ResponseWriter, r *http.Request) {
	items, _ := s.tm.ListAll(r.Context())
	s.render(w, r, "admin_testimonials.html", pageData{
		"Testimonials": items,
		"Error":        r.URL.Query().Get("error"),
	})
}

// AdminCreateTestimonial publishes a staff-written entry (live by default).
func (s *Server) AdminCreateTestimonial(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		if err := r.ParseForm(); err != nil {
			http.Redirect(w, r, "/admin/testimonials?error="+url.QueryEscape("Could not read form"), http.StatusSeeOther)
			return
		}
	}
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	photoPath, photoURL, msg := s.snapshotPhoto(r, nil)
	if msg != "" {
		http.Redirect(w, r, "/admin/testimonials?error="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}
	rating, _ := strconv.Atoi(r.FormValue("rating"))
	if rating == 0 {
		rating = 5
	}
	age, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("age")))
	t := &domain.Testimonial{
		Name: r.FormValue("name"), Age: age,
		PhotoPath: photoPath, PhotoURL: photoURL,
		Rating: rating, Message: r.FormValue("message"),
		Status: domain.TestimonialApproved,
	}
	if err := s.tm.Save(r.Context(), t); err != nil {
		http.Redirect(w, r, "/admin/testimonials?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/testimonials?toast="+url.QueryEscape("Testimonial added")+"&kind=success", http.StatusSeeOther)
}

// AdminTestimonialStatus moves an entry between pending/approved/hidden.
func (s *Server) AdminTestimonialStatus(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	status := domain.TestimonialStatus(strings.TrimSpace(r.FormValue("status")))
	if err := s.tm.SetStatus(r.Context(), id, status); err != nil {
		http.Redirect(w, r, "/admin/testimonials?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/testimonials?toast="+url.QueryEscape("Testimonial updated")+"&kind=success", http.StatusSeeOther)
}

// AdminDeleteTestimonial removes an entry permanently.
func (s *Server) AdminDeleteTestimonial(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = s.tm.Delete(r.Context(), id)
	http.Redirect(w, r, "/admin/testimonials?toast="+url.QueryEscape("Testimonial removed")+"&kind=success", http.StatusSeeOther)
}

// serveUploadDir serves a file from an upload subdirectory (traversal-safe).
func (s *Server) serveUploadDir(subdir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		file := chi.URLParam(r, "file")
		if file == "" || strings.Contains(file, "/") || strings.Contains(file, "\\") {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, filepath.Join(s.cfg.UploadDir, subdir, filepath.Clean(file)))
	}
}

// ServeTestimonialPhoto serves uploaded review photos (public).
func (s *Server) ServeTestimonialPhoto(w http.ResponseWriter, r *http.Request) {
	s.serveUploadDir("testimonials")(w, r)
}

// ServeAvatar serves uploaded profile pictures (public).
func (s *Server) ServeAvatar(w http.ResponseWriter, r *http.Request) {
	s.serveUploadDir("avatars")(w, r)
}
