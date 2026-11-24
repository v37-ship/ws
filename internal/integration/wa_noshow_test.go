//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// noShowFixtures seeds a branch with a service and therapist, then returns a
// helper that writes one booking for a customer at a distinct hour (the
// no-double-book exclusion makes hour spacing load-bearing).
func noShowFixtures(t *testing.T, pool *pgxpool.Pool) func(customer *uuid.UUID, status domain.BookingStatus, hour int) uuid.UUID {
	t.Helper()
	loc := insertLocation(t, pool, "No-show Branch")
	svc := insertService(t, pool, loc, "No-show Treatment", 150_000)
	staff := insertStaff(t, pool, loc, "No-show Therapist")
	seedAt := time.Date(2026, 12, 1, 9, 0, 0, 0, time.UTC)
	return func(customer *uuid.UUID, status domain.BookingStatus, hour int) uuid.UUID {
		return insertBooking(t, pool, bookingSeed{
			CustomerID: customer,
			LocationID: loc, ServiceID: svc, StaffID: staff,
			StartAt: seedAt.Add(time.Duration(hour) * time.Hour),
			Status:  string(status),
		})
	}
}

// TestCountNoShowsScopesToOneCustomer pins the repository read behind the
// reminder gate: only that customer's no_show rows count, every other status
// and every guest/no-account booking is ignored.
func TestCountNoShowsScopesToOneCustomer(t *testing.T) {
	pool := OpenTestDB(t)
	TruncateAll(t, pool)
	bookings := repo.NewBookingRepo(pool)
	ctx := context.Background()

	book := noShowFixtures(t, pool)
	serial := insertUser(t, pool, "serial-noshow@example.com", "")
	other := insertUser(t, pool, "other-noshow@example.com", "")

	book(&serial, domain.BookingNoShow, 0)
	book(&serial, domain.BookingNoShow, 1)
	book(&serial, domain.BookingConfirmed, 2)
	book(&serial, domain.BookingCancelled, 3)
	book(&other, domain.BookingNoShow, 4)
	book(nil, domain.BookingNoShow, 5) // guest: no customer to count it against

	if n, err := bookings.CountNoShows(ctx, serial); err != nil || n != 2 {
		t.Errorf("serial count = (%d, %v), want (2, nil); only no_show rows of that customer count", n, err)
	}
	if n, err := bookings.CountNoShows(ctx, other); err != nil || n != 1 {
		t.Errorf("other count = (%d, %v), want (1, nil)", n, err)
	}
	if n, err := bookings.CountNoShows(ctx, uuid.New()); err != nil || n != 0 {
		t.Errorf("unknown customer count = (%d, %v), want (0, nil)", n, err)
	}
}

// TestCancelRemindersForCustomerLeavesOtherMessages pins ruling R9: only
// pending remind:% rows joined through bookings.customer_id are cancelled. A
// review request for the same customer, an already-sent reminder and another
// customer's reminder all survive.
func TestCancelRemindersForCustomerLeavesOtherMessages(t *testing.T) {
	pool := OpenTestDB(t)
	TruncateAll(t, pool)
	wa := repo.NewWaRepo(pool)
	ctx := context.Background()

	book := noShowFixtures(t, pool)
	serial := insertUser(t, pool, "serial-cancel@example.com", "")
	other := insertUser(t, pool, "other-cancel@example.com", "")

	visitA := book(&serial, domain.BookingConfirmed, 0)
	visitB := book(&serial, domain.BookingConfirmed, 1)
	reviewVisit := book(&serial, domain.BookingCompleted, 2)
	sentVisit := book(&serial, domain.BookingConfirmed, 3)
	otherVisit := book(&other, domain.BookingConfirmed, 4)

	remindA := insertWaOutbox(t, pool, waOutboxSeed{
		Phone: "628111000001", TemplateKey: "booking_remind", DedupeKey: "remind:" + visitA.String(),
		BookingID: &visitA, ScheduledAt: time.Now().Add(time.Hour),
	})
	remindB := insertWaOutbox(t, pool, waOutboxSeed{
		Phone: "628111000001", TemplateKey: "booking_remind", DedupeKey: "remind:" + visitB.String(),
		BookingID: &visitB, ScheduledAt: time.Now().Add(2 * time.Hour),
	})
	reviewRow := insertWaOutbox(t, pool, waOutboxSeed{
		Phone: "628111000001", TemplateKey: "review_request", DedupeKey: "review:" + reviewVisit.String(),
		BookingID: &reviewVisit, ScheduledAt: time.Now().Add(3 * time.Hour),
	})
	sentRow := insertWaOutbox(t, pool, waOutboxSeed{
		Phone: "628111000001", TemplateKey: "booking_remind", Status: "sent", Attempts: 1,
		DedupeKey: "remind:" + sentVisit.String(), BookingID: &sentVisit,
	})
	otherRow := insertWaOutbox(t, pool, waOutboxSeed{
		Phone: "628222000002", TemplateKey: "booking_remind", DedupeKey: "remind:" + otherVisit.String(),
		BookingID: &otherVisit, ScheduledAt: time.Now().Add(4 * time.Hour),
	})

	n, err := wa.CancelRemindersForCustomer(ctx, serial)
	if err != nil {
		t.Fatalf("CancelRemindersForCustomer: %v", err)
	}
	if n != 2 {
		t.Errorf("cancelled %d rows, want 2 (the customer's pending reminders)", n)
	}

	survivors := map[string]uuid.UUID{
		"review_request": reviewRow,
		"sent reminder":  sentRow,
		"other customer": otherRow,
	}
	for name, id := range survivors {
		if got := readWaOutbox(t, pool, id).Status; got == "cancelled" {
			t.Errorf("%s row was cancelled, want it left alone", name)
		}
	}
	for name, id := range map[string]uuid.UUID{"reminder A": remindA, "reminder B": remindB} {
		if got := readWaOutbox(t, pool, id).Status; got != "cancelled" {
			t.Errorf("%s status = %q, want cancelled", name, got)
		}
	}

	// A customer with nothing pending is a quiet no-op.
	if n, err := wa.CancelRemindersForCustomer(ctx, uuid.New()); err != nil || n != 0 {
		t.Errorf("unknown customer cancel = (%d, %v), want (0, nil)", n, err)
	}
}
