package handlers

import (
	"net/http"

	"spa-booking/internal/http/htmx"
	"spa-booking/internal/i18n"
)

// PortalSendDigest posts the day's agenda to the staff WhatsApp group on
// demand. The 07:00 ticker normally does this once a day; this button is for
// "the group missed it" (a separate dedupe key, so the daily send is not
// disturbed).
func (s *Server) PortalSendDigest(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	lang := i18n.Resolve(r)
	message, kind := i18n.T(lang, "p.dg.sent"), "success"
	if s.digest == nil || !s.digest.Configured() {
		message, kind = i18n.T(lang, "p.dg.off"), "error"
	} else if sent, err := s.digest.Run(r.Context(), true); err != nil {
		s.log.Error("staff digest", "err", err)
		message, kind = "Could not queue the agenda", "error"
	} else if !sent {
		message, kind = i18n.T(lang, "p.dg.none"), "error"
	}
	if htmx.IsRequest(r) {
		s.toast(w, kind, message)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/portal", http.StatusSeeOther)
}
