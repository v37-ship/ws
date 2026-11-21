package handlers

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/http/htmx"
	"spa-booking/internal/repo"
	"spa-booking/internal/service"
)

// tvTokenActive is the kiosk's single data dependency. It is a variable so the
// handler tests can pin "valid / revoked / unknown" without a database (the
// repos are concrete pgx types, no interface seam), exactly like queueBookings.
var tvTokenActive = func(s *Server, r *http.Request, token string) (*domain.TVToken, error) {
	return s.tvTokens.ActiveByToken(r.Context(), token)
}

// tvTokenFrom resolves the ?t= kiosk token. Missing, unknown, revoked, and
// mismatched tokens all collapse to (nil, false): the caller renders one
// minimal page, so an outsider cannot tell a revoked link from a typo.
//
// ActiveByToken already excludes revoked rows in SQL, but the Go-side check is
// the actual gate: Active() makes revocation instant even for a row someone
// hands us out of band, and TokenOK is a constant-time compare so the token
// cannot be guessed by timing the endpoint.
func (s *Server) tvTokenFrom(r *http.Request) (*domain.TVToken, bool) {
	presented := r.URL.Query().Get("t")
	if presented == "" {
		return nil, false
	}
	tok, err := tvTokenActive(s, r, presented)
	if err != nil {
		// A miss is the normal bad-link case (and deliberately indistinguishable
		// from a revoked one); anything else is an outage the operator must see.
		// The presented token itself is never logged.
		if !errors.Is(err, repo.ErrNotFound) {
			s.log.Error("tv token lookup", "err", err)
		}
		return nil, false
	}
	if tok == nil {
		return nil, false
	}
	if !tok.Active() || !tok.TokenOK(presented) {
		return nil, false
	}
	return tok, true
}

// The pairing handshake's knobs. The code is short-lived and single-use; the
// pending cookie only has to outlive the code (it is the identity the 4s poll
// presents, not a credential); the device cookie is the durable credential,
// matching the 7-day session cookie's "long-lived but revocable" practice.
const (
	tvPendingCookie = "tv_pending"
	tvDeviceCookie  = "tv_device"
	tvPairingTTL    = 5 * time.Minute
	tvPendingTTL    = 15 * time.Minute
	tvDeviceTTL     = 30 * 24 * time.Hour
)

// tvPairingCreate / tvPairingByKey are the handshake's two repo seams, in the
// tvTokenActive style: the tests pin every state without a database.
var (
	tvPairingCreate = func(s *Server, r *http.Request, p *domain.TVPairing) error {
		return s.tvPairings.Create(r.Context(), p)
	}
	tvPairingByKey = func(s *Server, r *http.Request, key string) (*domain.TVPairing, error) {
		return s.tvPairings.ByPendingKey(r.Context(), key)
	}
)

// tvPairingCode mints the code the counter types: six digits from crypto/rand,
// zero-padded (leading zeros are unambiguous — the code is a string, never a
// number).
func tvPairingCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// tvPairingStart is what a bare /tv renders (ruling #7): it creates a pending
// pairing, hands the screen its code, and keeps the pending key in an HttpOnly
// cookie so the 4s poll can ask "paired yet?" without ever exposing identity
// in a URL.
//
// The pending key doubles as the device secret. The portal hashes it into the
// device row when the code is claimed (so the server keeps only the hash), and
// this screen already holds the plaintext — that is how the cookie is handed
// over without a secret ever travelling to the counter.
func (s *Server) tvPairingStart(w http.ResponseWriter, r *http.Request) {
	key, err := service.NewAccessToken()
	if err != nil {
		s.log.Error("tv pairing key", "err", err)
		s.render(w, r, "tv_invalid.html", pageData{})
		return
	}
	code, err := tvPairingCode()
	if err != nil {
		s.log.Error("tv pairing code", "err", err)
		s.render(w, r, "tv_invalid.html", pageData{})
		return
	}
	p := &domain.TVPairing{
		PendingKey: key,
		Code:       code,
		ExpiresAt:  tvClock().Add(tvPairingTTL),
	}
	if err := tvPairingCreate(s, r, p); err != nil {
		// A screen has nobody to read an error page: keep the minimal kiosk
		// page (a reload starts a fresh pairing) and leave a line for the ops
		// log. This is an outage, not a bad link, so it is worth a log line.
		s.log.Error("tv pairing create", "err", err)
		s.render(w, r, "tv_invalid.html", pageData{})
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: tvPendingCookie, Value: p.PendingKey, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: s.csrfSecure,
		MaxAge: int(tvPendingTTL.Seconds()),
	})
	s.render(w, r, "tv_pairing.html", pageData{"Code": p.Code, "State": "pending"})
}

// TVPairState is the pairing screen's 4s poll. It reads the pending cookie and
// answers one of three states, none of which carries any booking data:
//
//   - pending — a tiny "waiting" fragment (the screen keeps showing its code)
//   - paired  — the device cookie is Set and the page is sent back to /tv,
//     where the credential chain renders the real (redacted) view
//   - expired — the notice plus a start-over link; the stale pending cookie is
//     cleared so the next /tv is a clean start, never a loop on a dead key
func (s *Server) TVPairState(w http.ResponseWriter, r *http.Request) {
	key := ""
	if c, err := r.Cookie(tvPendingCookie); err == nil {
		key = c.Value
	}
	p, state, err := s.tvPairingState(r, key)
	if err != nil {
		// A DB blip is not an expired code: answer 500 so htmx keeps the last
		// fragment and the poll retries in 4s (TVQueuePartial's precedent).
		s.log.Error("tv pair state", "err", err)
		http.Error(w, "pairing unavailable", http.StatusInternalServerError)
		return
	}
	switch state {
	case "paired":
		// The TV presents its own pending key as the device secret; the id
		// comes from the row the portal just linked.
		http.SetCookie(w, &http.Cookie{
			Name: tvDeviceCookie, Value: domain.TVDeviceCookie(*p.DeviceID, p.PendingKey),
			Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
			Secure: s.csrfSecure, MaxAge: int(tvDeviceTTL.Seconds()),
		})
		htmx.Redirect(w, "/tv")
		w.WriteHeader(http.StatusNoContent)
	case "expired":
		http.SetCookie(w, &http.Cookie{Name: tvPendingCookie, Value: "", Path: "/", MaxAge: -1})
		s.renderPartial(w, r, "tv_pair_state.html", pageData{"State": "expired"})
	default:
		s.renderPartial(w, r, "tv_pair_state.html", pageData{"State": "pending"})
	}
}

// tvPairingState classifies the poll. A real lookup error is returned as one
// (the handler answers 500); a missing/dead key is "expired".
//
// Order matters: the handshake is completed by the CS consuming the code and
// linking the device, so a paired row is ALSO consumed. "Has a device" wins
// over every liveness check — otherwise the TV would be told its code expired
// exactly at the moment it was paired. Conversely, a consumed-but-unlinked row
// (the milliseconds between Consume and LinkDevice) is still pending, not
// expired: clearing the pending cookie there would orphan the device.
func (s *Server) tvPairingState(r *http.Request, key string) (*domain.TVPairing, string, error) {
	if key == "" {
		return nil, "expired", nil
	}
	p, err := tvPairingByKey(s, r, key)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, "expired", nil
		}
		return nil, "", err
	}
	if p == nil {
		return nil, "expired", nil
	}
	if p.DeviceID != nil {
		return p, "paired", nil
	}
	if !p.ExpiresAt.After(tvClock()) {
		return nil, "expired", nil
	}
	return p, "pending", nil
}
