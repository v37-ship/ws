package handlers

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/http/htmx"
	"spa-booking/internal/repo"

	"github.com/google/uuid"
)

// tvWIB is the kiosk's wall clock. Times and the "today" boundary are WIB,
// matching the portal dashboard's day scope; the TV never renders a raw UTC
// instant.
var tvWIB = time.FixedZone("WIB", 7*3600)

// tvClock is time.Now behind a variable so the band tests can pin "now"
// instead of depending on when the suite runs.
var tvClock = time.Now

// tvQueueBookings is the kiosk's single data dependency (the queueBookings
// precedent: the repos are concrete pgx types, so tests substitute the call
// rather than the database).
var tvQueueBookings = func(s *Server, ctx context.Context, f repo.FilterParams) ([]domain.Booking, error) {
	return s.book.ListFiltered(ctx, f)
}

// tvRow is the complete redaction contract: the TV's HTML is built from these
// five strings and nothing else. Never add a field here — if the screen needs
// new information, the design's redaction ruling (decision #4) must change
// first, and the no-PII render test must grow with it.
//
// StatusLabel carries a dictionary key ("tv.st.*"), not a rendered label: the
// builder is language-agnostic, and the template resolves the key so the same
// rows serve the EN and ID sweeps.
type tvRow struct {
	TimeLabel    string
	NameRedacted string
	ServiceName  string
	StaffFirst   string
	StatusLabel  string
}

// redactName turns a stored display name into "first + last initial":
//
//   - "Sari Wijaya" -> "Sari W." (last token's first rune, then a dot)
//   - "Sari Wijaya Putri" -> "Sari P." (only the last token's initial)
//   - "Sari" -> "Sari" (nothing to initial)
//   - "Sari W" -> "Sari W" (a one-rune surname is shown as-is: "W." would
//     invent punctuation the customer never gave us)
//   - "  Sari   Wijaya  " -> "Sari W." (whitespace collapsed)
//   - "" / "   " -> "" (the row then renders the name cell empty)
//
// Guest names are stored the same way, so they redact identically. Unicode is
// handled by rune, and pre-existing punctuation ("Sari W.") is preserved.
func redactName(full string) string {
	fields := strings.Fields(full)
	switch len(fields) {
	case 0:
		return ""
	case 1:
		return fields[0]
	}
	last := []rune(fields[len(fields)-1])
	if len(last) == 1 {
		return fields[0] + " " + string(last)
	}
	return fields[0] + " " + string(last[0]) + "."
}

// tvFirstName is the therapist column's rule: first token only, never a
// surname.
func tvFirstName(full string) string {
	fields := strings.Fields(full)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// tvTimeLabel renders the slot time in WIB, the only timezone the spa posts.
func tvTimeLabel(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.In(tvWIB).Format("15:04")
}

// tvStatusKeys is the kiosk's status whitelist AND its label set. A booking
// whose status is absent here (cancelled, no_show, expired, pending_payment)
// can never reach the TV — the filter and the label cannot drift apart because
// they are the same map.
var tvStatusKeys = map[domain.BookingStatus]string{
	domain.BookingConfirmed: "tv.st.conf",
	domain.BookingCheckedIn: "tv.st.arr",
	domain.BookingInService: "tv.st.svc",
	domain.BookingCompleted: "tv.st.done",
}

// tvNextCap is the "Berikutnya" strip's row budget.
const tvNextCap = 3

// tvQueueRows builds the kiosk's three bands:
//
//   - now  — in_service only, by start time ("Sedang dilayani")
//   - next — checked_in first (they are physically present), then future
//     confirmed by start time, capped at 3 ("Berikutnya")
//   - day  — every allowed status by start time (the full-day list)
//
// The repo filter supports a single status, so the status whitelist and the
// homecare exclusion are applied here, client-side, over today's branch
// rows — documented and test-pinned.
func (s *Server) tvQueueRows(ctx context.Context, loc uuid.UUID) (now, next, day []tvRow, err error) {
	nowWIB := tvClock().In(tvWIB)
	bookings, err := tvQueueBookings(s, ctx, repo.FilterParams{
		LocationID: loc,
		Date:       nowWIB.Format("2006-01-02"),
	})
	if err != nil {
		return nil, nil, nil, err
	}

	var inService, arrived, upcoming, listed []domain.Booking
	for _, b := range bookings {
		// Homecare never appears: nobody is in this waiting room (ruling #11).
		if b.Place == domain.PlaceHomecare {
			continue
		}
		if _, ok := tvStatusKeys[b.Status]; !ok {
			continue
		}
		listed = append(listed, b)
		switch b.Status {
		case domain.BookingInService:
			inService = append(inService, b)
		case domain.BookingCheckedIn:
			arrived = append(arrived, b)
		case domain.BookingConfirmed:
			upcoming = append(upcoming, b)
		}
	}

	tvSortByStart := func(rows []domain.Booking) {
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].StartAt.Equal(rows[j].StartAt) {
				return rows[i].ID.String() < rows[j].ID.String()
			}
			return rows[i].StartAt.Before(rows[j].StartAt)
		})
	}
	tvSortByStart(inService)
	tvSortByStart(arrived)
	tvSortByStart(upcoming)
	tvSortByStart(listed)

	band := make([]domain.Booking, 0, tvNextCap)
	for _, b := range arrived {
		if len(band) == tvNextCap {
			break
		}
		band = append(band, b)
	}
	for _, b := range upcoming {
		if len(band) == tvNextCap {
			break
		}
		// A confirmed slot whose time already passed belongs in the day list
		// only: the waiting room should look forward, not at a stale promise.
		if !b.StartAt.After(nowWIB) {
			continue
		}
		band = append(band, b)
	}

	return tvRows(inService), tvRows(band), tvRows(listed), nil
}

// tvRows maps filtered bookings onto the redacted view-model. This is the only
// place booking fields become TV fields — no template reads a domain.Booking.
func tvRows(bookings []domain.Booking) []tvRow {
	rows := make([]tvRow, 0, len(bookings))
	for _, b := range bookings {
		rows = append(rows, tvRow{
			TimeLabel:    tvTimeLabel(b.StartAt),
			NameRedacted: redactName(b.CustomerName),
			ServiceName:  b.ServiceName,
			StaffFirst:   tvFirstName(b.StaffName),
			StatusLabel:  tvStatusKeys[b.Status],
		})
	}
	return rows
}

// TVView is the public kiosk page. The credential is resolved inside the
// handler, so this route stays outside every auth middleware.
func (s *Server) TVView(w http.ResponseWriter, r *http.Request) {
	acc, mode := s.tvCredential(r)
	switch mode {
	case tvCredDeadDevice:
		// Ruling #7: a credential that can no longer authenticate is cleared and
		// the screen re-enters the handshake on its own — no counter visit, no
		// dark wall. tvPairingStart renders the code and sets a fresh pending
		// cookie.
		s.tvClearDeviceCookie(w)
		s.tvPairingStart(w, r)
		return
	case tvCredNone:
		s.tvPairingStart(w, r)
		return
	case tvCredBadToken, tvCredUnavailable:
		s.render(w, r, "tv_invalid.html", pageData{})
		return
	}
	// The layout rides the URL (the admin page shows both variants); the queue
	// is the same in both shells and only its placement changes. TVToken is the
	// shipped ?t= credential and stays empty for a paired screen, whose cookie
	// authenticates its poll.
	data := pageData{
		"TVToken": acc.Token,
		"Layout":  normalizeTVLayout(r.URL.Query().Get("layout")),
	}
	slides := tvLoadSlides(s, r.Context(), acc.LocationID)
	data["Services"] = slides.Services
	data["Testimonials"] = slides.Testimonials
	data["Food"] = slides.Food
	data["ReviewURL"] = slides.ReviewURL
	now, next, day, err := s.tvQueueRows(r.Context(), acc.LocationID)
	if err != nil {
		// The kiosk has no staff to read an error page; keep the shell and let
		// the 30s poll retry. The next poll replaces the notice on recovery.
		s.log.Error("tv queue", "err", err)
		data["TvError"] = true
	} else {
		data["Now"], data["Next"], data["Day"] = now, next, day
	}
	s.render(w, r, "tv_view.html", data)
}

// TVQueuePartial is the kiosk's polling endpoint (30s, the portal dashboard
// pattern). It authenticates by the same chain as the page: a paired screen
// sends its cookie, a token URL sends ?t=.
//
// A bad ?t= answers the minimal invalid page, so a revoked link goes dark
// instead of silently freezing on old rows. A missing or dead cookie instead
// asks htmx for a full reload: the pairing screen must never be swapped into
// the queue slot, and /tv is where the chain (and the cookie clear) runs. A
// credential we could not VERIFY is a 500, so the wall keeps its last good rows
// and retries in 30s.
func (s *Server) TVQueuePartial(w http.ResponseWriter, r *http.Request) {
	acc, mode := s.tvCredential(r)
	switch mode {
	case tvCredBadToken:
		s.render(w, r, "tv_invalid.html", pageData{})
		return
	case tvCredUnavailable:
		// The credential could not be verified (the device lookup already
		// logged the cause): answer 500 so htmx keeps the last good rows and
		// the poll retries in 30s.
		http.Error(w, "queue unavailable", http.StatusInternalServerError)
		return
	case tvCredDeadDevice, tvCredNone:
		if !htmx.IsRequest(r) {
			// A plain browser hit has no page to reload: keep the shipped face.
			s.render(w, r, "tv_invalid.html", pageData{})
			return
		}
		s.tvClearDeviceCookie(w)
		htmx.Refresh(w)
		return
	}
	now, next, day, err := s.tvQueueRows(r.Context(), acc.LocationID)
	if err != nil {
		// A 500 is deliberate: htmx's responseHandling drops 4xx/5xx swaps, so
		// the wall keeps its last good rows and the poll retries in 30s
		// (blanking the queue on a transient blip is the failure mode we want
		// least on a kiosk).
		s.log.Error("tv queue poll", "err", err)
		http.Error(w, "queue unavailable", http.StatusInternalServerError)
		return
	}
	s.renderPartial(w, r, "tv_queue_rows.html", pageData{"Now": now, "Next": next, "Day": day})
}
