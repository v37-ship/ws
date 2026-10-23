package handlers

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"spa-booking/internal/domain"
	"spa-booking/internal/http/htmx"
	"spa-booking/internal/http/middleware"
	"spa-booking/internal/repo"
	"spa-booking/internal/service"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// ClaimPayment records the customer's "Saya sudah bayar" signal.
func (s *Server) ClaimPayment(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	b, token, ok := s.bookingAccess(w, r, id)
	if !ok {
		return
	}
	u := middleware.UserFrom(r.Context())
	var actorID *uuid.UUID
	if u != nil && b.OwnedBy(u.ID) {
		actorID = &u.ID
	}
	if err := s.book.ClaimPaid(r.Context(), id, actorID); err != nil {
		s.redirectPay(w, r, b, token, "", err.Error())
		return
	}
	s.redirectPay(w, r, b, token, "We received your payment notice", "")
}

// UploadBookingReceipt lets the customer attach a transfer proof.
func (s *Server) UploadBookingReceipt(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, service.MaxReceiptBytes+(1<<20))
	if err := r.ParseMultipartForm(10 << 20); err != nil || !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	b, token, ok := s.bookingAccess(w, r, id)
	if !ok {
		return
	}
	file, hdr, err := r.FormFile("receipt")
	if err != nil {
		s.redirectPay(w, r, b, token, "", "Choose a receipt file first")
		return
	}
	path, msg := s.storeReceipt(file, hdr.Filename, hdr.Size, b.ID)
	if msg != "" {
		s.redirectPay(w, r, b, token, "", msg)
		return
	}
	u := middleware.UserFrom(r.Context())
	var actorID *uuid.UUID
	if u != nil {
		actorID = &u.ID
	}
	old := ""
	if p, err := s.book.GetPaymentForBooking(r.Context(), b.ID); err == nil && p != nil {
		old = p.ReceiptPath
	}
	if err := s.book.SetReceipt(r.Context(), b.ID, path, actorID); err != nil {
		s.log.Error("set receipt", "booking", b.ID, "err", err)
		s.redirectPay(w, r, b, token, "", "Could not save the receipt")
		return
	}
	s.removeReceipt(old, path)
	s.redirectPay(w, r, b, token, "Receipt uploaded", "")
}

func (s *Server) redirectPay(w http.ResponseWriter, r *http.Request, b *domain.Booking, token, success, errMsg string) {
	back := "/bookings/" + b.ID.String() + "/pay"
	q := url.Values{}
	if token != "" {
		q.Set("t", token)
	}
	if success != "" {
		q.Set("success", success)
	}
	if errMsg != "" {
		q.Set("error", errMsg)
	}
	if len(q) > 0 {
		back += "?" + q.Encode()
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// PortalConfirmPayment settles a manual payment (static QRIS or cash).
func (s *Server) PortalConfirmPayment(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	u := middleware.UserFrom(r.Context())
	if u == nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	method := domain.PaymentMethod(r.FormValue("method"))
	if method == "" {
		method = domain.PaymentMethodQRISStatic
	}
	reference := strings.TrimSpace(r.FormValue("reference"))
	note := strings.TrimSpace(r.FormValue("note"))
	if err := s.book.ConfirmManualPayment(r.Context(), id, method, reference, note, u.ID); err != nil {
		s.portalPaymentResult(w, r, id, "", err.Error())
		return
	}
	s.portalPaymentResult(w, r, id, "Payment confirmed", "")
}

func (s *Server) PortalRejectClaim(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	u := middleware.UserFrom(r.Context())
	if u == nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := s.book.RejectClaim(r.Context(), id, r.FormValue("note"), u.ID); err != nil {
		s.portalPaymentResult(w, r, id, "", err.Error())
		return
	}
	s.portalPaymentResult(w, r, id, "Payment claim rejected", "")
}

func (s *Server) portalPaymentResult(w http.ResponseWriter, r *http.Request, id uuid.UUID, success, errMsg string) {
	if htmx.IsRequest(r) {
		if errMsg != "" {
			s.toast(w, "error", errMsg)
		} else {
			s.toast(w, "success", success)
		}
		htmx.Refresh(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	target := "/portal/bookings/" + id.String()
	if errMsg != "" {
		target += "?error=" + url.QueryEscape(errMsg)
	} else {
		target += "?success=" + url.QueryEscape(success)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// PortalSendWhatsApp queues an explicit staff message through the WA API.
func (s *Server) PortalSendWhatsApp(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	u := middleware.UserFrom(r.Context())
	if u == nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	kind := r.FormValue("kind")
	if err := s.book.SendManualWA(r.Context(), id, kind, r.FormValue("message"), u.ID); err != nil {
		s.portalPaymentResult(w, r, id, "", err.Error())
		return
	}
	s.portalPaymentResult(w, r, id, "WhatsApp message queued", "")
}

// PortalUploadReceipt lets staff attach a receipt received via WhatsApp.
func (s *Server) PortalUploadReceipt(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, service.MaxReceiptBytes+(1<<20))
	if err := r.ParseMultipartForm(10 << 20); err != nil || !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	b, err := s.book.GetBooking(r.Context(), id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "Could not load booking", http.StatusInternalServerError)
		return
	}
	file, hdr, err := r.FormFile("receipt")
	if err != nil {
		s.portalPaymentResult(w, r, id, "", "Choose a receipt file first")
		return
	}
	path, msg := s.storeReceipt(file, hdr.Filename, hdr.Size, b.ID)
	if msg != "" {
		s.portalPaymentResult(w, r, id, "", msg)
		return
	}
	u := middleware.UserFrom(r.Context())
	var actorID *uuid.UUID
	if u != nil {
		actorID = &u.ID
	}
	old := ""
	if p, err := s.book.GetPaymentForBooking(r.Context(), b.ID); err == nil && p != nil {
		old = p.ReceiptPath
	}
	if err := s.book.SetReceipt(r.Context(), b.ID, path, actorID); err != nil {
		s.portalPaymentResult(w, r, id, "", "Could not save the receipt")
		return
	}
	s.removeReceipt(old, path)
	s.portalPaymentResult(w, r, id, "Receipt uploaded", "")
}

// PortalServeReceipt streams the receipt to staff only (never public).
func (s *Server) PortalServeReceipt(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p, err := s.book.GetPaymentForBooking(r.Context(), id)
	if err != nil || p == nil || p.ReceiptPath == "" {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(s.cfg.UploadDir, filepath.FromSlash(p.ReceiptPath))
	rel, err := filepath.Rel(s.cfg.UploadDir, full)
	if err != nil || strings.HasPrefix(rel, "..") || strings.HasPrefix(rel, string(filepath.Separator)+"..") {
		http.NotFound(w, r)
		return
	}
	if _, err := os.Stat(full); err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("inline; filename=%q", "receipt-"+strings.ReplaceAll(p.OrderID, "/", "-")+filepath.Ext(full)))
	http.ServeFile(w, r, full)
}

// storeReceipt writes an uploaded receipt under uploads/receipts/<booking>/.
// It returns the relative path or a user-facing error message.
func (s *Server) storeReceipt(file io.Reader, original string, size int64, bookingID uuid.UUID) (string, string) {
	stored, err := service.ValidateReceiptName(original, size)
	if err != nil {
		return "", err.Error()
	}
	dir := filepath.Join(s.cfg.UploadDir, "receipts", bookingID.String())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.log.Error("receipt mkdir", "err", err)
		return "", "Could not store the receipt"
	}
	dst, err := os.Create(filepath.Join(dir, stored))
	if err != nil {
		s.log.Error("receipt create", "err", err)
		return "", "Could not store the receipt"
	}
	written, err := io.Copy(dst, io.LimitReader(file, service.MaxReceiptBytes+1))
	if err != nil || written > service.MaxReceiptBytes {
		_ = dst.Close()
		_ = os.Remove(dst.Name())
		if err != nil {
			return "", "Could not store the receipt"
		}
		return "", "Receipt must be under 5 MB"
	}
	_ = dst.Close()
	return filepath.ToSlash(filepath.Join("receipts", bookingID.String(), stored)), ""
}

// removeReceipt deletes the previous file after a successful replacement.
func (s *Server) removeReceipt(old, next string) {
	if old == "" || old == next || !strings.HasPrefix(old, "receipts/") {
		return
	}
	_ = os.Remove(filepath.Join(s.cfg.UploadDir, filepath.FromSlash(old)))
}
