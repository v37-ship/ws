package handlers

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"spa-booking/internal/http/middleware"
)

// AdminPayments is the admin-only static QRIS configuration page.
func (s *Server) AdminPayments(w http.ResponseWriter, r *http.Request) {
	s.renderAdminPayments(w, r, r.URL.Query().Get("success"), r.URL.Query().Get("error"))
}

func (s *Server) renderAdminPayments(w http.ResponseWriter, r *http.Request, success, errMsg string) {
	settings, err := s.paySettings.Get(r.Context())
	if err != nil {
		s.log.Error("payment settings", "err", err)
		http.Error(w, "Could not load payment settings", http.StatusInternalServerError)
		return
	}
	image := "/static/img/qris-wangga.jpeg?v=" + staticVersion
	if settings.QRISImagePath != "" {
		image = "/uploads/" + settings.QRISImagePath
	}
	s.render(w, r, "admin_payments.html", pageData{
		"Settings":  settings,
		"QRISImage": image,
		"Success":   success,
		"Error":     errMsg,
		"Uploaded":  settings.QRISImagePath != "",
	})
}

func (s *Server) AdminUploadQRIS(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 3<<20)
	if err := r.ParseMultipartForm(10 << 20); err != nil || !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	u := middleware.UserFrom(r.Context())
	if u == nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	file, hdr, err := r.FormFile("qris")
	if err != nil {
		s.renderAdminPayments(w, r, "", "Choose a QRIS image first")
		return
	}
	path, msg := s.storeUpload(file, file, hdr.Filename, hdr.Size, "qris")
	if msg != "" {
		s.renderAdminPayments(w, r, "", msg)
		return
	}
	old := ""
	if settings, err := s.paySettings.Get(r.Context()); err == nil && settings != nil {
		old = settings.QRISImagePath
	}
	if err := s.paySettings.SetQRISImage(r.Context(), path, u.ID); err != nil {
		s.log.Error("save qris setting", "err", err)
		_ = os.Remove(filepath.Join(s.cfg.UploadDir, filepath.FromSlash(path)))
		s.renderAdminPayments(w, r, "", "Could not save the QRIS image")
		return
	}
	s.removeQRIS(old)
	http.Redirect(w, r, "/admin/payments?success="+url.QueryEscape("QRIS image updated"), http.StatusSeeOther)
}

func (s *Server) AdminResetQRIS(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	u := middleware.UserFrom(r.Context())
	if u == nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	old := ""
	if settings, err := s.paySettings.Get(r.Context()); err == nil && settings != nil {
		old = settings.QRISImagePath
	}
	if err := s.paySettings.SetQRISImage(r.Context(), "", u.ID); err != nil {
		s.renderAdminPayments(w, r, "", "Could not reset the QRIS image")
		return
	}
	s.removeQRIS(old)
	http.Redirect(w, r, "/admin/payments?success="+url.QueryEscape("Using the built-in QRIS image"), http.StatusSeeOther)
}

func (s *Server) removeQRIS(old string) {
	if old == "" || !strings.HasPrefix(old, "qris/") {
		return
	}
	_ = os.Remove(filepath.Join(s.cfg.UploadDir, filepath.FromSlash(old)))
}
