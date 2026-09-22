package handlers

import (
	"errors"
	"net/http"

	"spa-booking/internal/domain"
	"spa-booking/internal/http/middleware"
	"spa-booking/internal/repo"

	"github.com/google/uuid"
)

// bookingAccess resolves booking access for session owners, staff, and
// guests carrying a management token. It writes the failure response itself:
// ok=false means the handler must stop. The returned token is non-empty
// only for token-authed guests (for link passthrough).
func (s *Server) bookingAccess(w http.ResponseWriter, r *http.Request, id uuid.UUID) (*domain.Booking, string, bool) {
	b, err := s.book.GetBooking(r.Context(), id)
	if errors.Is(err, repo.ErrNotFound) {
		http.NotFound(w, r)
		return nil, "", false
	}
	if err != nil {
		s.log.Error("booking fetch", "id", id, "err", err)
		http.Error(w, "Could not load booking", http.StatusInternalServerError)
		return nil, "", false
	}
	u := middleware.UserFrom(r.Context())
	if u != nil && (u.Role.IsStaff() || b.OwnedBy(u.ID)) {
		return b, "", true
	}
	if t := r.URL.Query().Get("t"); b.TokenOK(t) {
		return b, t, true
	}
	if u == nil {
		http.Redirect(w, r, "/login?next="+r.URL.Path, http.StatusSeeOther)
		return nil, "", false
	}
	http.Error(w, "forbidden", http.StatusForbidden)
	return nil, "", false
}
