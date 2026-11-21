package handlers

import (
	"errors"
	"net/http"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"
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

// TVView and TVQueuePartial live in tv_queue.go: they need the redacted
// view-model builder, and keeping the kiosk data path in one file makes the
// "no PII leaves this package" boundary obvious.
