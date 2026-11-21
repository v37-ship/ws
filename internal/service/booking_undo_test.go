package service

import (
	"testing"

	"spa-booking/internal/domain"
)

// undoLogs builds a newest-first audit trail from action texts, the shape
// BookingRepo.ListLogs hands the service.
func undoLogs(actions ...string) []domain.BookingLog {
	entries := make([]domain.BookingLog, 0, len(actions))
	for _, a := range actions {
		entries = append(entries, domain.BookingLog{Action: a})
	}
	return entries
}

// previousStatusFromLogs is the whole safety story of undo: it only derives a
// previous status when the newest status entry still matches what the booking
// says. Anything else must refuse (ruling 7), because a concurrent change means
// the undo would overwrite someone else's work.
func TestPreviousStatusFromLogs(t *testing.T) {
	cases := []struct {
		name    string
		actions []string
		current domain.BookingStatus
		want    domain.BookingStatus
		ok      bool
	}{
		{
			name:    "normal chain returns the status before the current one",
			actions: []string{"Status changed to completed", "Status changed to in_service"},
			current: domain.BookingCompleted,
			want:    domain.BookingInService,
			ok:      true,
		},
		{
			name:    "no entries refuses",
			actions: nil,
			current: domain.BookingConfirmed,
			ok:      false,
		},
		{
			name:    "most recent status not matching current refuses",
			actions: []string{"Status changed to completed", "Status changed to in_service"},
			current: domain.BookingConfirmed,
			ok:      false,
		},
		{
			name: "interleaved non-status entries are skipped",
			actions: []string{
				"Status changed to completed",
				"Catatan: bayi tenang",
				"Rescheduled appointment",
				"Status changed to checked_in",
			},
			current: domain.BookingCompleted,
			want:    domain.BookingCheckedIn,
			ok:      true,
		},
		{
			name:    "a lone status entry has no predecessor",
			actions: []string{"Status changed to confirmed"},
			current: domain.BookingConfirmed,
			ok:      false,
		},
		{
			name:    "text that is not a known status is ignored",
			actions: []string{"Status changed to completed", "Status changed to nonsense", "Status changed to in_service"},
			current: domain.BookingCompleted,
			want:    domain.BookingInService,
			ok:      true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := previousStatusFromLogs(undoLogs(c.actions...), c.current)
			if ok != c.ok {
				t.Fatalf("ok = %v, want %v", ok, c.ok)
			}
			if got != c.want {
				t.Errorf("previous status = %q, want %q", got, c.want)
			}
		})
	}
}
