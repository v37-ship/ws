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

// TVView is the public kiosk page. It is deliberately not behind any auth
// middleware: the token is the credential, and a staff session must never be
// required on shared hardware.
func (s *Server) TVView(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.tvTokenFrom(r); !ok {
		s.render(w, r, "tv_invalid.html", pageData{})
		return
	}
	// Placeholder shell: the redacted queue view (and its layouts) land in the
	// next tasks; the token gate is what this route proves today.
	s.render(w, r, "tv_view.html", nil)
}

// TVQueuePartial is the kiosk's polling endpoint (30s, mirroring the portal
// dashboard). A bad token answers the same minimal page as the page route so a
// revoked TV goes dark instead of silently freezing on old rows.
func (s *Server) TVQueuePartial(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.tvTokenFrom(r); !ok {
		s.render(w, r, "tv_invalid.html", pageData{})
		return
	}
	s.renderPartial(w, r, "tv_placeholder.html", nil)
}
