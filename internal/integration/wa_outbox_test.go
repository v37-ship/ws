//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"spa-booking/internal/repo"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// waOutboxSeed is the writable subset insertWaOutbox accepts. Zero values:
// Status "pending", TemplateKey "test", ScheduledAt now. A non-empty DedupeKey
// is the row identity the retry must never touch (ruling R6).
type waOutboxSeed struct {
	Phone       string
	TemplateKey string
	Status      string
	Attempts    int
	LastError   string
	DedupeKey   string
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
		`INSERT INTO wa_outbox (id, phone, template_key, status, attempts, last_error, dedupe_key, scheduled_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		id, seed.Phone, seed.TemplateKey, seed.Status, seed.Attempts, seed.LastError, dedupe, seed.ScheduledAt,
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
	changed, err := wa.RetryFailed(ctx, failedID)
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
		changed, err := wa.RetryFailed(ctx, id)
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

	if changed, err := wa.RetryFailed(ctx, uuid.New()); err != nil || changed {
		t.Errorf("unknown id = (changed=%v err=%v), want (false, nil)", changed, err)
	}
}

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
