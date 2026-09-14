package handlers

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"spa-booking/internal/domain"
	"spa-booking/internal/service"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func (s *Server) CareersPage(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.career.ListJobs(r.Context(), true)
	if err != nil {
		http.Error(w, "failed to load jobs", http.StatusInternalServerError)
		return
	}
	s.render(w, r, "careers.html", pageData{
		"Jobs":  jobs,
		"Error": r.URL.Query().Get("error"),
	})
}

func (s *Server) ApplyForJob(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(service.MaxCVBytes + (1 << 20)); err != nil {
		http.Redirect(w, r, "/careers?error="+url.QueryEscape("Could not read form"), http.StatusSeeOther)
		return
	}
	if !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	jobID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	job, err := s.career.JobByID(r.Context(), jobID)
	if err != nil || !job.Active {
		http.Redirect(w, r, "/careers?error="+url.QueryEscape("This opening is no longer available"), http.StatusSeeOther)
		return
	}
	name := strings.TrimSpace(r.FormValue("full_name"))
	phone := strings.TrimSpace(r.FormValue("phone"))
	if name == "" || phone == "" {
		http.Redirect(w, r, "/careers?error="+url.QueryEscape("Name and phone are required"), http.StatusSeeOther)
		return
	}
	file, header, err := r.FormFile("cv")
	if err != nil {
		http.Redirect(w, r, "/careers?error="+url.QueryEscape("Please attach your CV (PDF/DOC/JPG/PNG, max 5 MB)"), http.StatusSeeOther)
		return
	}
	defer file.Close()
	stored, err := service.ValidateCVName(header.Filename, header.Size)
	if err != nil {
		http.Redirect(w, r, "/careers?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	dst, err := os.Create(filepath.Join(s.cfg.UploadDir, stored))
	if err != nil {
		s.log.Error("cv save", "err", err)
		http.Error(w, "failed to save CV", http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(dst, file); err != nil {
		_ = dst.Close()
		s.log.Error("cv save", "err", err)
		http.Error(w, "failed to save CV", http.StatusInternalServerError)
		return
	}
	_ = dst.Close()
	app := &domain.JobApplication{
		JobID: job.ID, FullName: name, Email: strings.TrimSpace(r.FormValue("email")),
		Phone: phone, Message: strings.TrimSpace(r.FormValue("message")),
		CVFilename: stored, CVOriginalName: header.Filename,
	}
	if err := s.career.SaveApplication(r.Context(), app); err != nil {
		s.log.Error("application save", "err", err)
		http.Error(w, "failed to submit application", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/careers?toast="+url.QueryEscape("Application sent! Our team will contact you soon.")+"&kind=success", http.StatusSeeOther)
}

func (s *Server) AdminJobs(w http.ResponseWriter, r *http.Request) {
	jobs, _ := s.career.ListJobs(r.Context(), false)
	locs, _ := s.catalog.ListLocations(r.Context(), false)
	s.render(w, r, "admin_jobs.html", pageData{
		"Jobs": jobs, "Locations": locs,
		"Error": r.URL.Query().Get("error"),
	})
}

func (s *Server) AdminCreateJob(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	var locID *uuid.UUID
	if v := strings.TrimSpace(r.FormValue("location_id")); v != "" {
		if id, err := uuid.Parse(v); err == nil {
			locID = &id
		}
	}
	if _, err := s.career.CreateJob(r.Context(),
		r.FormValue("title"), locID,
		domain.EmploymentType(r.FormValue("employment_type")),
		r.FormValue("description"), r.FormValue("requirements")); err != nil {
		http.Redirect(w, r, "/admin/jobs?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/jobs?toast="+url.QueryEscape("Job posted")+"&kind=success", http.StatusSeeOther)
}

func (s *Server) AdminToggleJob(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	if id, err := uuid.Parse(chi.URLParam(r, "id")); err == nil {
		_ = s.career.ToggleJobActive(r.Context(), id)
	}
	http.Redirect(w, r, "/admin/jobs", http.StatusSeeOther)
}

func (s *Server) AdminJobApplications(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	job, err := s.career.JobByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	apps, _ := s.career.ListApplications(r.Context(), id)
	s.render(w, r, "admin_job_applications.html", pageData{"Job": job, "Applications": apps})
}

func (s *Server) AdminDownloadCV(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	app, err := s.career.ApplicationByID(r.Context(), id)
	if err != nil || app.CVFilename == "" || strings.Contains(app.CVFilename, "/") {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.cfg.UploadDir, app.CVFilename))
}
