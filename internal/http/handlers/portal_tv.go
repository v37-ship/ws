package handlers

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"spa-booking/internal/domain"
	"spa-booking/internal/http/middleware"
	"spa-booking/internal/i18n"
	"spa-booking/internal/repo"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// The counter's half of the TV pairing handshake. A screen shows a six-digit
// code; the CS types it here with the branch (and an optional device name) and
// the handshake closes: the device is minted, linked to the pairing, and the
// TV's next poll receives its cookie. Only device metadata ever renders on
// these pages — no booking data.

const (
	failTVPair      = "Could not pair the TV. Try again."
	failTVDevices   = "Could not load the paired TVs. Refresh the page and try again."
	failTVDevRevoke = "Could not revoke that TV. Try again."
	failTVDevGone   = "That TV is no longer listed. Refresh the page."
	failTVDevID     = "Unknown TV device"
	msgTVDevRevoke  = "TV revoked"
)

// The handshake's seams, in the tvTokenList/tvTokenRevoke style: the repos are
// concrete pgx types, so handler tests substitute the calls instead of opening
// a database.
var (
	tvPairingConsume = func(s *Server, r *http.Request, code string, loc uuid.UUID) (*domain.TVPairing, error) {
		return s.tvPairings.Consume(r.Context(), code, loc)
	}
	tvPairingLink = func(s *Server, r *http.Request, pairingID, deviceID uuid.UUID) error {
		return s.tvPairings.LinkDevice(r.Context(), pairingID, deviceID)
	}
	tvDeviceCreate = func(s *Server, r *http.Request, d *domain.TVDevice) error {
		return s.tvDevices.Create(r.Context(), d)
	}
	tvDeviceList = func(s *Server, r *http.Request) ([]domain.TVDevice, error) {
		return s.tvDevices.ListActive(r.Context())
	}
	tvDeviceRevoke = func(s *Server, r *http.Request, id uuid.UUID) error {
		return s.tvDevices.Revoke(r.Context(), id)
	}
)

// PortalTVPage is the pairing desk: the code form plus the list of paired
// screens. CS sees the list read-only; the revoke buttons render for admins
// only (the handler refuses CS anyway — the template is the second gate).
func (s *Server) PortalTVPage(w http.ResponseWriter, r *http.Request) {
	locs, err := tvActiveLocations(s, r)
	if err != nil {
		s.log.Error("tv pair locations", "err", err)
		s.staffFail(w, r, http.StatusInternalServerError, failTVDevices)
		return
	}
	devices, err := tvDeviceList(s, r)
	if err != nil {
		s.log.Error("tv devices", "err", err)
		s.staffFail(w, r, http.StatusInternalServerError, failTVDevices)
		return
	}
	locNames := make(map[string]string, len(locs))
	for _, l := range locs {
		locNames[l.ID.String()] = l.Name
	}
	s.render(w, r, "portal_tv.html", pageData{
		"Devices":   devices,
		"Locations": locs,
		"LocNames":  locNames,
		"Success":   r.URL.Query().Get("success"),
	})
}

// PortalTVPair claims a code and mints the screen's device.
//
// The order is load-bearing: Consume atomically binds the branch and burns the
// code, LinkDevice closes the handshake the TV polls for (ruling #6), and only
// then is the screen live. Every code miss — wrong, expired, already consumed —
// is one message (tv.pr.bad): the repo collapses them, and the form must not
// become a code oracle.
func (s *Server) PortalTVPair(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !s.requireCSRF(r) {
		s.staffFail(w, r, http.StatusBadRequest, failSession)
		return
	}
	u := middleware.UserFrom(r.Context())
	if u == nil {
		s.staffFail(w, r, http.StatusForbidden, failSession)
		return
	}
	lang := i18n.Resolve(r)

	// Ruling #5: the branch always comes from the person at the counter — no
	// user has a branch binding — and it must be one of today's active
	// branches, or the device row would fail its foreign key.
	locID, err := uuid.Parse(strings.TrimSpace(r.FormValue("location_id")))
	if err != nil {
		s.staffFail(w, r, http.StatusBadRequest, i18n.T(lang, "tv.pr.noloc"))
		return
	}
	locs, err := tvActiveLocations(s, r)
	if err != nil {
		s.log.Error("tv pair locations", "err", err)
		s.staffFail(w, r, http.StatusInternalServerError, failTVPair)
		return
	}
	locName := ""
	for _, l := range locs {
		if l.ID == locID {
			locName = l.Name
			break
		}
	}
	if locName == "" {
		s.staffFail(w, r, http.StatusBadRequest, i18n.T(lang, "tv.pr.noloc"))
		return
	}

	code := strings.TrimSpace(r.FormValue("code"))
	if !tvSixDigits(code) {
		s.staffFail(w, r, http.StatusBadRequest, i18n.T(lang, "tv.pr.bad"))
		return
	}

	pairing, err := tvPairingConsume(s, r, code, locID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			// One honest answer for wrong, expired and consumed codes.
			s.staffFail(w, r, http.StatusBadRequest, i18n.T(lang, "tv.pr.bad"))
			return
		}
		s.log.Error("tv pairing consume", "err", err)
		s.staffFail(w, r, http.StatusInternalServerError, failTVPair)
		return
	}
	if pairing == nil {
		s.staffFail(w, r, http.StatusBadRequest, i18n.T(lang, "tv.pr.bad"))
		return
	}

	// Ruling #8: the CS names the device; unnamed screens still have to be
	// tellable apart, hence the branch-and-date fallback. The cap duplicates
	// the form's maxlength: a crafted POST must not bloat the list either.
	label := strings.TrimSpace(r.FormValue("label"))
	if label == "" {
		label = "TV · " + locName + " · " + tvClock().In(tvWIB).Format("02 Jan 2006")
	} else if runes := []rune(label); len(runes) > 60 {
		label = string(runes[:60])
	}

	// The cross-task contract: the device secret IS the pairing's pending key,
	// which the TV already holds in its HttpOnly cookie. Only the hash is
	// stored (ruling #9), and the TV's next poll hands the plaintext back as
	// the tv_device cookie — a fresh random secret here would leave the screen
	// permanently unauthenticated.
	device := &domain.TVDevice{
		SecretHash: domain.TVSecretHash(pairing.PendingKey),
		Label:      label,
		LocationID: locID,
		PairedBy:   &u.ID,
	}
	if err := tvDeviceCreate(s, r, device); err != nil {
		s.log.Error("tv device create", "err", err)
		s.staffFail(w, r, http.StatusInternalServerError, failTVPair)
		return
	}
	if err := tvPairingLink(s, r, pairing.ID, device.ID); err != nil {
		// The TV polls the pairing, so an unlinked device can never be
		// claimed: revoke it rather than leave an orphan credential on the
		// list. The revoke is best effort — the 500 already reports the fault.
		s.log.Error("tv pairing link", "err", err)
		if rerr := tvDeviceRevoke(s, r, device.ID); rerr != nil {
			s.log.Error("tv orphan device revoke", "err", rerr)
		}
		s.staffFail(w, r, http.StatusInternalServerError, failTVPair)
		return
	}

	s.redirectHTMX(w, r, "/portal/tv?success="+url.QueryEscape(label+" "+i18n.T(lang, "tv.pr.ok")))
}

// PortalTVDeviceRevoke kills a paired screen. It is admin-only: the portal
// page renders the control for admins alone, and this in-handler check is the
// real gate (requirePortalAdmin — every /portal route admits CS). The same
// handler serves the admin page's form; the success target follows the
// namespace the POST arrived on, never a query parameter.
func (s *Server) PortalTVDeviceRevoke(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !s.requireCSRF(r) {
		s.staffFail(w, r, http.StatusBadRequest, failSession)
		return
	}
	if _, ok := s.requirePortalAdmin(r); !ok {
		s.staffFail(w, r, http.StatusForbidden, "Only an admin can revoke a TV.")
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		s.staffFail(w, r, http.StatusBadRequest, failTVDevID)
		return
	}
	back := "/portal/tv"
	if strings.HasPrefix(r.URL.Path, "/admin/") {
		back = "/admin/tv"
	}
	if err := tvDeviceRevoke(s, r, id); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			s.staffFail(w, r, http.StatusNotFound, failTVDevGone)
			return
		}
		s.log.Error("tv device revoke", "err", err)
		s.staffFail(w, r, http.StatusInternalServerError, failTVDevRevoke)
		return
	}
	s.redirectHTMX(w, r, back+"?success="+url.QueryEscape(msgTVDevRevoke))
}

// tvSixDigits is the code's whole shape rule: exactly six ASCII digits. The
// pairing screen zero-pads, so leading zeros are real digits, and the code is
// always handled as a string.
func tvSixDigits(code string) bool {
	if len(code) != 6 {
		return false
	}
	for i := 0; i < len(code); i++ {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}
