package service

import (
	"context"
	"testing"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
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
		{
			// A path that both moves the status and logs it as an activity row
			// must not make undo pick a bogus intermediate: the newest entry is
			// the current status, the next one (same status) reduces to it.
			name: "the same status logged twice in a row still resolves to that status",
			actions: []string{
				"Status changed to checked_in",
				"Status changed to confirmed",
				"Status changed to confirmed",
				"Status changed to pending_payment",
			},
			current: domain.BookingCheckedIn,
			want:    domain.BookingConfirmed,
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

// statusLogSpy captures the activity rows a creation path writes. It satisfies
// the slice of BookingRepo logCreationStatus needs.
type statusLogSpy struct {
	entries []domain.BookingLog
}

func (spy *statusLogSpy) LogActivity(_ context.Context, bookingID uuid.UUID, actorID *uuid.UUID, action, notes string) error {
	spy.entries = append(spy.entries, domain.BookingLog{
		BookingID: bookingID, UserID: actorID, Action: action, Notes: notes,
	})
	return nil
}

// StatusLogAction is the one place the audit copy is spelled: the repo writes
// it for every transition, so undo's parser must accept it verbatim.
func TestStatusLogAction(t *testing.T) {
	for _, status := range []domain.BookingStatus{
		domain.BookingPendingPayment, domain.BookingConfirmed, domain.BookingCheckedIn,
		domain.BookingInService, domain.BookingCompleted, domain.BookingCancelled,
		domain.BookingExpired, domain.BookingNoShow,
	} {
		want := "Status changed to " + string(status)
		if got := domain.StatusLogAction(status); got != want {
			t.Errorf("StatusLogAction(%q) = %q, want %q", status, got, want)
		}
	}
}

// A booking's first manual move — the check-in of a walk-in is the common one —
// is only undoable when the creation path recorded the initial status in the
// same "Status changed to X" form every later transition uses. Creation used to
// log only "Booking created…", so the newest status entry was the move itself
// and undo found no predecessor.
func TestLogCreationStatusMakesFirstMoveUndoable(t *testing.T) {
	cases := []struct {
		name     string
		created  domain.BookingStatus
		moved    domain.BookingStatus
		wantPrev domain.BookingStatus
	}{
		{
			name:     "walk-in cash booking is created confirmed",
			created:  domain.BookingConfirmed,
			moved:    domain.BookingCheckedIn,
			wantPrev: domain.BookingConfirmed,
		},
		{
			name:     "gateway booking is created pending_payment",
			created:  domain.BookingPendingPayment,
			moved:    domain.BookingExpired,
			wantPrev: domain.BookingPendingPayment,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			spy := &statusLogSpy{}
			actor := uuid.New()
			b := &domain.Booking{ID: uuid.New(), Status: c.created}
			logCreationStatus(context.Background(), spy, b, &actor)

			if len(spy.entries) != 1 {
				t.Fatalf("creation trail = %d rows, want 1", len(spy.entries))
			}
			row := spy.entries[0]
			if want := domain.StatusLogAction(c.created); row.Action != want {
				t.Errorf("creation row action = %q, want %q", row.Action, want)
			}
			if row.BookingID != b.ID {
				t.Errorf("creation row booking = %v, want %v", row.BookingID, b.ID)
			}
			if row.UserID == nil || *row.UserID != actor {
				t.Errorf("creation row actor = %v, want %v", row.UserID, actor)
			}
			if row.Notes != "" {
				t.Errorf("creation row notes = %q, want empty", row.Notes)
			}

			// Newest-first, as ListLogs returns them: the move first, then the
			// creation trail, with the human-readable row interleaved.
			logs := undoLogs(
				domain.StatusLogAction(c.moved),
				"Booking created by CS (Cash Payment - Services)",
				row.Action,
			)
			got, ok := previousStatusFromLogs(logs, c.moved)
			if !ok {
				t.Fatalf("undo found no predecessor after the first move")
			}
			if got != c.wantPrev {
				t.Errorf("previous status = %q, want %q", got, c.wantPrev)
			}
		})
	}
}

// The manual-payment confirm already moves pending_payment → confirmed through
// UpdateStatus, which writes the status row itself; the copy row next to it is
// skipped by the parser. With the creation row in place the chain stays
// undoable one step further, and nothing is logged twice.
func TestManualConfirmKeepsStatusChainUndoable(t *testing.T) {
	logs := undoLogs(
		domain.StatusLogAction(domain.BookingCheckedIn),
		"Manual payment confirmed (QRIS)",
		domain.StatusLogAction(domain.BookingConfirmed),
		"Booking created online (manual QRIS pending)",
		domain.StatusLogAction(domain.BookingPendingPayment),
	)
	got, ok := previousStatusFromLogs(logs, domain.BookingCheckedIn)
	if !ok {
		t.Fatalf("undo found no predecessor after confirm + first move")
	}
	if got != domain.BookingConfirmed {
		t.Errorf("previous status = %q, want %q", got, domain.BookingConfirmed)
	}

	// Undoing the confirm itself lands back on the creation status.
	got, ok = previousStatusFromLogs(logs[2:], domain.BookingConfirmed)
	if !ok {
		t.Fatalf("undo found no predecessor for the confirm itself")
	}
	if got != domain.BookingPendingPayment {
		t.Errorf("previous status = %q, want %q", got, domain.BookingPendingPayment)
	}
}
