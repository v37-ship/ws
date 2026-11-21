package handlers

import (
	"net/http"
	"net/url"
	"strings"

	"spa-booking/internal/domain"
	"spa-booking/internal/http/middleware"
	"spa-booking/internal/service"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// Admin TV link management. The kiosk itself has no session; this page is the
// only place the links exist, and it stays admin-only (cross-cutting setup, the
// same split as QRIS and WhatsApp settings).

const (
	failTVList   = "Could not load the TV links. Refresh the page and try again."
	failTVGen    = "Could not generate the TV link. Try again."
	failTVRevoke = "Could not revoke the TV link. Try again."
	msgTVGen     = "TV link generated"
	msgTVRevoke  = "TV link revoked"
)

// The four data seams follow the queueBookings precedent: the repos are
// concrete pgx types, so handler tests swap the calls instead of opening a DB.
var (
	tvTokenList = func(s *Server, r *http.Request) ([]domain.TVToken, error) {
		return s.tvTokens.ListActive(r.Context())
	}
	tvTokenCreate = func(s *Server, r *http.Request, tok *domain.TVToken) error {
		return s.tvTokens.Create(r.Context(), tok)
	}
	tvTokenRevoke = func(s *Server, r *http.Request, id uuid.UUID) error {
		return s.tvTokens.Revoke(r.Context(), id)
	}
	tvActiveLocations = func(s *Server, r *http.Request) ([]domain.Location, error) {
		return s.catalog.ListLocations(r.Context(), true)
	}
)

// AdminTVPage lists live kiosk links and their full URLs, and offers a
// generate form scoped to a branch (ruling: the token is location-bound).
func (s *Server) AdminTVPage(w http.ResponseWriter, r *http.Request) {
	list, err := tvTokenList(s, r)
	if err != nil {
		s.log.Error("tv tokens", "err", err)
		s.staffFail(w, r, http.StatusInternalServerError, failTVList)
		return
	}
	locs, err := tvActiveLocations(s, r)
	if err != nil {
		s.log.Error("tv locations", "err", err)
		s.staffFail(w, r, http.StatusInternalServerError, failTVList)
		return
	}
	// The token list names its branch from the same active-location set the
	// form offers, so an operator with two screens can tell them apart.
	locNames := make(map[string]string, len(locs))
	for _, l := range locs {
		locNames[l.ID.String()] = l.Name
	}
	s.render(w, r, "admin_tv.html", pageData{
		"Tokens":    list,
		"Locations": locs,
		"LocNames":  locNames,
		"Success":   r.URL.Query().Get("success"),
		"Error":     r.URL.Query().Get("error"),
		"HasDrawer": true,
	})
}

// AdminTVGenerate mints a kiosk token. The secret comes from the same
// crypto/rand hex generator guest bookings use, so the shape is one convention
// across the app.
func (s *Server) AdminTVGenerate(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if middleware.UserFrom(r.Context()) == nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	locID, err := uuid.Parse(strings.TrimSpace(r.FormValue("location_id")))
	if err != nil {
		s.staffFail(w, r, http.StatusBadRequest, "Choose a branch for this TV")
		return
	}
	token, err := service.NewAccessToken()
	if err != nil {
		s.log.Error("tv token gen", "err", err)
		s.staffFail(w, r, http.StatusInternalServerError, failTVGen)
		return
	}
	tok := domain.TVToken{
		Token:      token,
		Label:      strings.TrimSpace(r.FormValue("label")),
		LocationID: locID,
	}
	if err := tvTokenCreate(s, r, &tok); err != nil {
		s.log.Error("tv token create", "err", err)
		s.staffFail(w, r, http.StatusInternalServerError, failTVGen)
		return
	}
	s.redirectHTMX(w, r, "/admin/tv?success="+url.QueryEscape(msgTVGen))
}

// AdminTVRevoke kills a link. The TV's next poll is already invalid, so the
// screen goes dark without waiting for anything to expire.
func (s *Server) AdminTVRevoke(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if middleware.UserFrom(r.Context()) == nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.staffFail(w, r, http.StatusBadRequest, "Unknown TV link")
		return
	}
	if err := tvTokenRevoke(s, r, id); err != nil {
		s.log.Error("tv token revoke", "err", err)
		s.staffFail(w, r, http.StatusInternalServerError, failTVRevoke)
		return
	}
	s.redirectHTMX(w, r, "/admin/tv?success="+url.QueryEscape(msgTVRevoke))
}
