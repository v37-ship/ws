package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"
)

// DigestService posts the day's agenda to the staff WhatsApp group. It reads
// the same booking list the portal queue does, so the group never sees a
// different schedule from the one on screen.
type DigestService struct {
	book    *BookingService
	wa      Notifier
	group   string
	baseURL string
	hour    int
	log     *slog.Logger
}

func NewDigestService(book *BookingService, wa Notifier, group, baseURL string, hour int, log *slog.Logger) *DigestService {
	return &DigestService{book: book, wa: wa, group: group, baseURL: baseURL, hour: hour, log: log}
}

// Due reports whether the daily digest should be attempted now. The once-a-day
// dedupe key makes catch-up safe, so a late start still gets the agenda out.
func (s *DigestService) Due(now time.Time) bool {
	return now.In(wib).Hour() >= s.hour
}

// Configured reports whether a staff group is reachable at all.
func (s *DigestService) Configured() bool {
	return s.wa != nil && strings.TrimSpace(s.group) != ""
}

// Run queues the agenda for today. It returns sent=false when the group is not
// configured, the day's digest is already queued, or nothing was sent. force
// bypasses the once-a-day key (the portal button).
func (s *DigestService) Run(ctx context.Context, force bool) (bool, error) {
	if !s.Configured() {
		return false, nil
	}
	now := time.Now().In(wib)
	payload, err := s.build(ctx, now)
	if err != nil {
		return false, err
	}
	sent, err := s.wa.Enqueue(ctx, s.group, "staff_digest", payload, nil, digestKey(now, force), time.Time{})
	if err != nil {
		return false, err
	}
	if sent {
		s.log.Info("staff digest queued", "date", now.Format("2006-01-02"), "bookings", len(payload["items"].([]any)))
	}
	return sent, nil
}

// digestKey is the dedupe key that makes the daily send happen once. The
// manual button gets its own key so a staff retry never blocks tomorrow's
// digest, and never duplicates one that already went out.
func digestKey(now time.Time, force bool) string {
	day := now.In(wib).Format("2006-01-02")
	if force {
		return fmt.Sprintf("digest:manual:%s:%d", day, time.Now().UnixNano())
	}
	return "digest:" + day
}

// build assembles today's agenda, tomorrow's count and the payment follow-ups.
func (s *DigestService) build(ctx context.Context, now time.Time) (map[string]any, error) {
	today, err := s.book.ListFiltered(ctx, repo.FilterParams{Date: now.Format("2006-01-02"), Sort: "upcoming", Limit: 200})
	if err != nil {
		return nil, err
	}
	tomorrow, err := s.book.ListFiltered(ctx, repo.FilterParams{Date: now.AddDate(0, 0, 1).Format("2006-01-02"), Sort: "upcoming", Limit: 200})
	if err != nil {
		return nil, err
	}
	_, claims, err := s.book.PaymentCounts(ctx)
	if err != nil {
		// A digest without the follow-up counts is still useful.
		s.log.Warn("digest payment counts", "err", err)
		claims = 0
	}
	payload := digestPayload(today, tomorrow, claims, now)
	payload["portal_url"] = s.baseURL + "/portal/bookings"
	return payload, nil
}

// digestPayload shapes the message: one line per appointment, the claims
// waiting for review, and how full tomorrow already is.
func digestPayload(today, tomorrow []domain.Booking, claims int, now time.Time) map[string]any {
	items := make([]any, 0, len(today))
	for _, b := range today {
		if !digestStatus(b.Status) {
			continue
		}
		items = append(items, map[string]any{
			"time":    b.StartAt.In(wib).Format("15:04"),
			"service": b.ServiceName,
			"staff":   b.StaffName,
			"who":     firstName(digestName(b)),
			"place":   digestPlace(&b),
			"pay":     digestPay(&b),
		})
	}
	count := 0
	for _, b := range tomorrow {
		if digestStatus(b.Status) {
			count++
		}
	}
	return map[string]any{
		"date":     formatDayWIB(now),
		"items":    items,
		"claims":   claims,
		"tomorrow": count,
	}
}

// digestStatus keeps the agenda to appointments that are still real.
func digestStatus(st domain.BookingStatus) bool {
	switch st {
	case domain.BookingCancelled, domain.BookingExpired, domain.BookingNoShow:
		return false
	}
	return true
}

// digestName prefers the booking's own snapshot of who is coming.
func digestName(b domain.Booking) string {
	if b.IsGuest() {
		return b.GuestName
	}
	return b.CustomerName
}

// digestPlace marks homecare visits so the bidan knows where to go.
func digestPlace(b *domain.Booking) string {
	if b.Place != domain.PlaceHomecare {
		return ""
	}
	place := "🏠"
	if b.DistanceKm != nil {
		place += " " + bandLabel(b)
	}
	return place
}

// digestPay flags the follow-up: an unpaid or unverified booking.
func digestPay(b *domain.Booking) string {
	switch b.PaymentStatus {
	case domain.PaymentPaid:
		return ""
	case domain.PaymentExpired, domain.PaymentFailed:
		return "BAYAR " + strings.ToUpper(string(b.PaymentStatus))
	}
	return "BELUM BAYAR"
}

// firstName shortens a display name for the group copy.
func firstName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Bunda"
	}
	if i := strings.IndexByte(name, ' '); i > 0 {
		return name[:i]
	}
	return name
}
