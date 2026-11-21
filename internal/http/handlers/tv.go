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

	"github.com/google/uuid"
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

	// tvDeviceTouchEvery is how stale a device's last_seen must be before the
	// next request rewrites it. The throttle lives in the UPDATE's predicate
	// (repo.TVDeviceRepo.TouchLastSeen), so the 30s poll stamps liveness at most
	// once a minute without any caller-side bookkeeping.
	tvDeviceTouchEvery = time.Minute
)

// The credential chain's two device seams, in the tvTokenActive style: the
// repos are concrete pgx types, so the tests pin every state without a
// database.
var (
	tvDeviceByID = func(s *Server, r *http.Request, id uuid.UUID) (*domain.TVDevice, error) {
		return s.tvDevices.ByID(r.Context(), id)
	}
	tvDeviceTouch = func(s *Server, r *http.Request, id uuid.UUID, every time.Duration) error {
		return s.tvDevices.TouchLastSeen(r.Context(), id, every)
	}
)

// tvCredMode is what the resolution chain found. The zero value is the
// no-credential case, so a caller that never ran the chain starts a pairing
// rather than leaking a view.
type tvCredMode int

const (
	// tvCredNone: nothing was presented — start a pairing (ruling #7).
	tvCredNone tvCredMode = iota
	// tvCredLive: a live device cookie or ?t= token; tvAccess says which.
	tvCredLive
	// tvCredDeadDevice: a tv_device cookie was presented and failed. The
	// screen is cleared and re-enters the handshake by itself.
	tvCredDeadDevice
	// tvCredBadToken: a ?t= token was presented and failed. Unlike a dead
	// cookie this keeps the invalid page: someone minted that URL, so
	// silently re-opening pairing would hide a retired link.
	tvCredBadToken
	// tvCredUnavailable: the credential could not be verified (a DB blip).
	// Not dead — clearing it would cost the wall a counter visit for a
	// transient outage.
	tvCredUnavailable
)

// tvAccess is the location-scoped credential a kiosk page renders from. Token
// stays empty for a paired device (the poll URL then carries no secret at
// all); DeviceID stays zero for the shipped ?t= path. Templates never see a
// domain.TVDevice or a domain.TVToken.
type tvAccess struct {
	LocationID uuid.UUID
	Token      string
	DeviceID   uuid.UUID
}

// tvCredential resolves the kiosk's one credential (ruling #7's order):
//
//  1. a live tv_device cookie → the view (and a throttled last-seen stamp)
//  2. a live ?t= token → the view (the shipped path, unchanged)
//  3. a presented-but-dead device cookie → pairing screen, cookie cleared
//  4. no credential at all → pairing screen
//  5. a presented-but-bad ?t= token → the invalid page (unchanged)
//
// The device branch is tried first so retiring a token URL cannot black out a
// screen that still holds a valid device; the token branch then owns the URL
// that was deliberately opened, which is why a bad token never self-heals into
// a pairing.
func (s *Server) tvCredential(r *http.Request) (*tvAccess, tvCredMode) {
	deadDevice := false
	if c, err := r.Cookie(tvDeviceCookie); err == nil {
		acc, mode := s.tvDeviceAccess(r, c.Value)
		switch mode {
		case tvCredLive:
			return acc, mode
		case tvCredUnavailable:
			return nil, mode
		default:
			deadDevice = true
		}
	}
	if r.URL.Query().Has("t") {
		tok, ok := s.tvTokenFrom(r)
		if !ok {
			return nil, tvCredBadToken
		}
		return &tvAccess{LocationID: tok.LocationID, Token: tok.Token}, tvCredLive
	}
	if deadDevice {
		return nil, tvCredDeadDevice
	}
	return nil, tvCredNone
}

// tvDeviceAccess verifies one tv_device cookie value: parse, look up, and
// constant-time compare the presented secret with the stored hash. Only a
// definitive failure (garbage, unknown, revoked, wrong secret) is "dead"; a
// lookup error is an outage and is reported as such.
func (s *Server) tvDeviceAccess(r *http.Request, raw string) (*tvAccess, tvCredMode) {
	id, secret, ok := domain.ParseTVDeviceCookie(raw)
	if !ok {
		return nil, tvCredDeadDevice
	}
	d, err := tvDeviceByID(s, r, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, tvCredDeadDevice
		}
		// Never log the cookie value: the id alone is a lookup key, and a
		// transient blip must not read as a revocation.
		s.log.Error("tv device lookup", "err", err)
		return nil, tvCredUnavailable
	}
	if d == nil || !d.Active() || !d.SecretOK(secret) {
		return nil, tvCredDeadDevice
	}
	// Liveness for the portal's device list. A failed stamp is diagnostics,
	// never a reason to blank the wall.
	if err := tvDeviceTouch(s, r, d.ID, tvDeviceTouchEvery); err != nil {
		s.log.Error("tv device last seen", "err", err)
	}
	return &tvAccess{LocationID: d.LocationID, DeviceID: d.ID}, tvCredLive
}

// tvClearDeviceCookie expires the tv_device cookie. MaxAge < 0 renders as
// "Max-Age=0" — the same clear the pair-state poll uses for a stale pending
// key.
func (s *Server) tvClearDeviceCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: tvDeviceCookie, Value: "", Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: s.csrfSecure, MaxAge: -1,
	})
}

// tvClearPendingCookie expires the tv_pending cookie with the same path and
// flags the start sets, so every clear is a real deletion (not a lookalike
// cookie scoped somewhere else).
func (s *Server) tvClearPendingCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: tvPendingCookie, Value: "", Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: s.csrfSecure, MaxAge: -1,
	})
}

// tvPairPath is where the handshake sends the screen next: a fresh /tv start.
// Rotate is the default, so it appends nothing; only a queue-only bookmark is
// carried through, or a re-pairing queue screen would land in the rotating
// shell.
func tvPairPath(r *http.Request) string {
	if normalizeTVLayout(r.URL.Query().Get("layout")) == "queue" {
		return "/tv?layout=queue"
	}
	return "/tv"
}

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
	s.render(w, r, "tv_pairing.html", pageData{
		"Code": p.Code, "State": "pending",
		"Layout": normalizeTVLayout(r.URL.Query().Get("layout")),
	})
}

// TVPairState is the pairing screen's 4s poll. It reads the pending cookie and
// answers one of three states, none of which carries any booking data:
//
//   - pending — a tiny "waiting" fragment (the screen keeps showing its code)
//   - paired  — the device cookie is Set and the page is sent back to /tv,
//     where the credential chain renders the real (redacted) view
//   - expired — the poll re-arms itself: the stale pending cookie is cleared
//     and the answer is the same 204 + HX-Redirect used by the paired branch,
//     so the screen loads a FRESH pairing (new code, new 5-minute TTL) within
//     one 4s tick instead of waiting for someone with the TV remote. A
//     non-HTMX hit (a human on the endpoint) still gets the notice fragment,
//     which is the only path that renders it.
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
		// The handshake is over: the pending key is no longer a credential the
		// screen should keep presenting alongside its device cookie.
		s.tvClearPendingCookie(w)
		htmx.Redirect(w, tvPairPath(r))
		w.WriteHeader(http.StatusNoContent)
	case "expired":
		s.tvClearPendingCookie(w)
		if htmx.IsRequest(r) {
			// Self-repair: the poll drives the screen back to a bare /tv, which
			// mints a fresh code. No remote control, no counter visit.
			htmx.Redirect(w, tvPairPath(r))
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.renderPartial(w, r, "tv_pair_state.html", pageData{
			"State":  "expired",
			"Layout": normalizeTVLayout(r.URL.Query().Get("layout")),
		})
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
