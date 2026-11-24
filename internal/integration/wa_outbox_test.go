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

// waOutboxSeed is the writable subset insertWaOutbox accepts. Zero values:
// Status "pending", TemplateKey "test", ScheduledAt now. A non-empty DedupeKey
// is the row identity the retry must never touch (ruling R6); BookingID links
// the message to a visit so the retry can see its current status.
type waOutboxSeed struct {
	Phone       string
	TemplateKey string
	Status      string
	Attempts    int
	LastError   string
	DedupeKey   string
	BookingID   *uuid.UUID
	ScheduledAt time.Time
}

func insertWaOutbox(t *testing.T, pool *pgxpool.Pool, seed waOutboxSeed) uuid.UUID {
	t.Helper()
	if seed.Phone == "" {
		t.Fatal("insertWaOutbox: Phone is required")
	}
	if seed.Status == "" {
		seed.Status = "pending"
	}
	if seed.TemplateKey == "" {
		seed.TemplateKey = "test"
	}
	if seed.ScheduledAt.IsZero() {
		seed.ScheduledAt = time.Now()
	}
	var dedupe *string
	if seed.DedupeKey != "" {
		dedupe = &seed.DedupeKey
	}
	id := uuid.New()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx,
		`INSERT INTO wa_outbox (id, phone, template_key, status, attempts, last_error, dedupe_key, booking_id, scheduled_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		id, seed.Phone, seed.TemplateKey, seed.Status, seed.Attempts, seed.LastError, dedupe, seed.BookingID, seed.ScheduledAt,
	); err != nil {
		t.Fatalf("insertWaOutbox: %v", err)
	}
	return id
}

// waOutboxRow is the subset the retry assertions read back.
type waOutboxRow struct {
	Status      string
	Attempts    int
	LastError   string
	ScheduledAt time.Time
	DedupeKey   *string
}

func readWaOutbox(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) waOutboxRow {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var row waOutboxRow
	if err := pool.QueryRow(ctx,
		`SELECT status, attempts, last_error, scheduled_at, dedupe_key FROM wa_outbox WHERE id=$1`, id,
	).Scan(&row.Status, &row.Attempts, &row.LastError, &row.ScheduledAt, &row.DedupeKey); err != nil {
		t.Fatalf("read wa_outbox %s: %v", id, err)
	}
	return row
}

// TestWaRetryFailedFlipsOnlyRetryableRows pins ruling R6: a failed row flips to
// pending with attempts reset, the error cleared and scheduled_at moved to now;
// sent/sending/pending, a cancelled row (a deliberate withdrawal) and an
// unknown id are no-ops. The dedupe key is the row identity and must survive.
func TestWaRetryFailedFlipsOnlyRetryableRows(t *testing.T) {
	pool := OpenTestDB(t)
	TruncateAll(t, pool)
	wa := repo.NewWaRepo(pool)
	ctx := context.Background()

	past := time.Now().Add(-2 * time.Hour)
	failedID := insertWaOutbox(t, pool, waOutboxSeed{
		Phone: "6281111111111", Status: "failed", Attempts: 4,
		LastError: "gave up after 4 attempts: boom", DedupeKey: "retry:failed",
		ScheduledAt: past,
	})
	untouched := map[string]uuid.UUID{
		"cancelled": insertWaOutbox(t, pool, waOutboxSeed{
			Phone: "6282222222222", Status: "cancelled", DedupeKey: "retry:cancelled",
			ScheduledAt: past,
		}),
		"sent":    insertWaOutbox(t, pool, waOutboxSeed{Phone: "6283333333333", Status: "sent", Attempts: 1}),
		"sending": insertWaOutbox(t, pool, waOutboxSeed{Phone: "6284444444444", Status: "sending", Attempts: 1}),
		"pending": insertWaOutbox(t, pool, waOutboxSeed{Phone: "6285555555555", Status: "pending"}),
	}

	before := time.Now().Add(-time.Second)
	changed, err := wa.RetryFailed(ctx, failedID, time.Now())
	if err != nil {
		t.Fatalf("failed retry: %v", err)
	}
	if !changed {
		t.Fatal("failed retry changed = false, want true")
	}
	row := readWaOutbox(t, pool, failedID)
	if row.Status != "pending" {
		t.Errorf("failed status = %q, want pending", row.Status)
	}
	if row.Attempts != 0 {
		t.Errorf("failed attempts = %d, want 0", row.Attempts)
	}
	if row.LastError != "" {
		t.Errorf("failed last_error = %q, want empty", row.LastError)
	}
	if row.ScheduledAt.Before(before) {
		t.Errorf("failed scheduled_at = %v, want now (>= %v)", row.ScheduledAt, before)
	}
	if row.DedupeKey == nil || *row.DedupeKey != "retry:failed" {
		t.Errorf("failed dedupe_key = %v, want %q (row identity must survive)", row.DedupeKey, "retry:failed")
	}

	for name, id := range untouched {
		changed, err := wa.RetryFailed(ctx, id, time.Now())
		if err != nil {
			t.Fatalf("%s retry: %v", name, err)
		}
		if changed {
			t.Errorf("%s row was retried, want a no-op", name)
		}
		if row := readWaOutbox(t, pool, id); row.Status != name {
			t.Errorf("%s status = %q after a refused retry, want %q", name, row.Status, name)
		}
	}

	if changed, err := wa.RetryFailed(ctx, uuid.New(), time.Now()); err != nil || changed {
		t.Errorf("unknown id = (changed=%v err=%v), want (false, nil)", changed, err)
	}
}

// TestWaRetryFailedRefusesClosedBookings pins the stale-lifecycle guard: a
// failed row whose booking has since become cancelled/expired/no_show would
// tell the customer about a visit that no longer holds, so the retry is
// refused. A reminder whose visit is completed or already past is refused too
// (ruling R16), while a review row on a completed visit stays retryable. A row
// with no booking is always retryable. ListRecent must report the joined status
// and start for the page.
func TestWaRetryFailedRefusesClosedBookings(t *testing.T) {
	pool := OpenTestDB(t)
	TruncateAll(t, pool)
	wa := repo.NewWaRepo(pool)
	ctx := context.Background()

	loc := insertLocation(t, pool, "Retry Branch")
	svc := insertService(t, pool, loc, "Retry Treatment", 150_000)
	staff := insertStaff(t, pool, loc, "Retry Therapist")

	seedAt := time.Now().Add(24 * time.Hour)
	booking := func(status domain.BookingStatus, offset time.Duration) uuid.UUID {
		return insertBooking(t, pool, bookingSeed{
			LocationID: loc, ServiceID: svc, StaffID: staff,
			StartAt: seedAt.Add(offset),
			Status:  string(status),
		})
	}
	failedRow := func(dedupe string, bookingID *uuid.UUID) uuid.UUID {
		return insertWaOutbox(t, pool, waOutboxSeed{
			Phone: "6281111111111", Status: "failed", Attempts: 3,
			LastError: "boom", DedupeKey: dedupe, BookingID: bookingID,
		})
	}

	refused := map[string]uuid.UUID{}
	for i, status := range []domain.BookingStatus{domain.BookingCancelled, domain.BookingExpired, domain.BookingNoShow} {
		b := booking(status, time.Duration(i)*time.Hour)
		refused[string(status)] = failedRow("stale:"+string(status), &b)
	}
	// Ruling R16: a reminder is retryable only while the visit is ahead.
	completedReminder := failedRow("remind:done", ptrUUID(booking(domain.BookingCompleted, time.Hour)))
	refused["completed reminder"] = completedReminder
	pastReminder := failedRow("remind:past", ptrUUID(booking(domain.BookingConfirmed, -25*time.Hour)))
	refused["past reminder"] = pastReminder

	confirmedRow := failedRow("remind:future", ptrUUID(booking(domain.BookingConfirmed, 3*time.Hour)))
	futureReminder := failedRow("remind:future2", ptrUUID(booking(domain.BookingConfirmed, 2*time.Hour)))
	completedReview := failedRow("review:done", ptrUUID(booking(domain.BookingCompleted, -time.Hour)))
	noBookingRow := failedRow("none", nil)

	// The failed view carries every failed row: the recent window would only
	// hold the newest 20, so this is the operator's reachable list (R14).
	failures, err := wa.ListFailed(ctx, 100)
	if err != nil {
		t.Fatalf("ListFailed: %v", err)
	}
	if len(failures) != len(refused)+4 {
		t.Fatalf("ListFailed returned %d rows, want %d", len(failures), len(refused)+4)
	}
	for _, row := range failures {
		if row.Status != "failed" {
			t.Errorf("ListFailed returned a %q row", row.Status)
		}
	}

	for name, id := range refused {
		changed, err := wa.RetryFailed(ctx, id, time.Now())
		if err != nil {
			t.Fatalf("%s retry: %v", name, err)
		}
		if changed {
			t.Errorf("%s: retry changed = true, want refused", name)
		}
		if row := readWaOutbox(t, pool, id); row.Status != "failed" {
			t.Errorf("%s: status = %q after a refused retry, want failed", name, row.Status)
		}
	}

	for name, id := range map[string]uuid.UUID{
		"confirmed reminder": confirmedRow,
		"future reminder":    futureReminder,
		"completed review":   completedReview,
		"no booking":         noBookingRow,
	} {
		changed, err := wa.RetryFailed(ctx, id, time.Now())
		if err != nil {
			t.Fatalf("%s retry: %v", name, err)
		}
		if !changed {
			t.Errorf("%s row: retry changed = false, want allowed", name)
			continue
		}
		if row := readWaOutbox(t, pool, id); row.Status != "pending" {
			t.Errorf("%s row: status = %q, want pending", name, row.Status)
		}
	}

	// The page decides from one read: recent rows carry the joined status and
	// start time.
	recent, err := wa.ListRecent(ctx, 100)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	got := map[uuid.UUID]repo.WaOutboxItem{}
	for _, row := range recent {
		got[row.ID] = row
	}
	if got[refused["cancelled"]].BookingStatus != "cancelled" {
		t.Errorf("recent cancelled booking status = %q, want cancelled", got[refused["cancelled"]].BookingStatus)
	}
	if got[confirmedRow].BookingStatus != "confirmed" {
		t.Errorf("recent confirmed booking status = %q, want confirmed", got[confirmedRow].BookingStatus)
	}
	if got[confirmedRow].BookingStart.IsZero() {
		t.Error("recent confirmed booking start is zero, want the joined visit time")
	}
	if got[noBookingRow].BookingStatus != "" {
		t.Errorf("recent no-booking status = %q, want empty", got[noBookingRow].BookingStatus)
	}
	if !got[noBookingRow].BookingStart.IsZero() {
		t.Errorf("recent no-booking start = %v, want zero", got[noBookingRow].BookingStart)
	}

}

func ptrUUID(id uuid.UUID) *uuid.UUID { return &id }

// TestWaQueueSummaryCounts pins the header counters: pending and failed are
// counted by status, and a cancelled row is not a failure.
func TestWaQueueSummaryCounts(t *testing.T) {
	pool := OpenTestDB(t)
	TruncateAll(t, pool)
	wa := repo.NewWaRepo(pool)
	ctx := context.Background()

	if pending, failed, err := wa.QueueSummary(ctx); err != nil || pending != 0 || failed != 0 {
		t.Fatalf("empty outbox summary = (%d, %d, %v), want (0, 0, nil)", pending, failed, err)
	}

	for _, seed := range []waOutboxSeed{
		{Phone: "6281000000001", Status: "pending"},
		{Phone: "6281000000002", Status: "pending", ScheduledAt: time.Now().Add(time.Hour)},
		{Phone: "6281000000003", Status: "failed"},
		{Phone: "6281000000004", Status: "cancelled"},
		{Phone: "6281000000005", Status: "sent"},
		{Phone: "6281000000006", Status: "sending"},
	} {
		insertWaOutbox(t, pool, seed)
	}

	pending, failed, err := wa.QueueSummary(ctx)
	if err != nil {
		t.Fatalf("QueueSummary: %v", err)
	}
	if pending != 2 || failed != 1 {
		t.Errorf("summary = (pending %d, failed %d), want (2, 1); cancelled/sent/sending must not count",
			pending, failed)
	}
}
