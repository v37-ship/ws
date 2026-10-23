package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"spa-booking/internal/repo"
	"spa-booking/internal/whatsapp"

	"github.com/google/uuid"
)

type fakeWaStore struct {
	batches [][]repo.WaOutboxItem

	sent    []uuid.UUID
	failed  []uuid.UUID
	retried []uuid.UUID
	retryAt time.Time
	errMsg  string
}

func (f *fakeWaStore) ClaimDue(context.Context, int) ([]repo.WaOutboxItem, error) {
	if len(f.batches) == 0 {
		return nil, nil
	}
	b := f.batches[0]
	f.batches = f.batches[1:]
	return b, nil
}

func (f *fakeWaStore) MarkSent(_ context.Context, id uuid.UUID) error {
	f.sent = append(f.sent, id)
	return nil
}

func (f *fakeWaStore) RetryLater(_ context.Context, id uuid.UUID, nextAt time.Time, errMsg string) error {
	f.retried = append(f.retried, id)
	f.retryAt = nextAt
	f.errMsg = errMsg
	return nil
}

func (f *fakeWaStore) MarkFailed(_ context.Context, id uuid.UUID, errMsg string) error {
	f.failed = append(f.failed, id)
	f.errMsg = errMsg
	return nil
}

func (f *fakeWaStore) RequeueSending(context.Context) (int64, error) { return 0, nil }

func (f *fakeWaStore) Enqueue(context.Context, string, string, map[string]any, *uuid.UUID, string, time.Time) (bool, error) {
	return true, nil
}

type sendCall struct{ phone, text string }

type fakeSender struct {
	calls []sendCall
	id    string
	err   error
}

func (f *fakeSender) SendText(_ context.Context, phone, text string) (string, error) {
	f.calls = append(f.calls, sendCall{phone: phone, text: text})
	return f.id, f.err
}

func testItem(phone, key, text string, attempts, max int) repo.WaOutboxItem {
	payload := []byte(`{}`)
	if text != "" {
		payload = []byte(`{"text":"` + text + `"}`)
	}
	return repo.WaOutboxItem{
		ID:          uuid.New(),
		Phone:       phone,
		TemplateKey: key,
		Payload:     payload,
		Attempts:    attempts,
		MaxAttempts: max,
	}
}

func newTestWaService(store waStore, sender WhatsAppSender, enabled bool) *WaService {
	return NewWaService(store, sender, slog.New(slog.NewTextHandler(io.Discard, nil)), enabled, 0)
}

func TestWaDrainMockMode(t *testing.T) {
	item := testItem("0812-3456-7890", "test", "halo", 1, 5)
	store := &fakeWaStore{batches: [][]repo.WaOutboxItem{{item}}}
	sender := &fakeSender{}
	svc := newTestWaService(store, sender, false)
	svc.now = func() time.Time { return time.Unix(0, 0) }

	svc.Drain(context.Background())

	if len(store.sent) != 1 || store.sent[0] != item.ID {
		t.Fatalf("sent = %v, want %s", store.sent, item.ID)
	}
	if len(sender.calls) != 0 {
		t.Fatalf("mock mode must not call the API, calls = %v", sender.calls)
	}
}

func TestWaDrainSendsNormalizedPhone(t *testing.T) {
	item := testItem("0812-3456-7890", "test", "halo", 1, 5)
	store := &fakeWaStore{batches: [][]repo.WaOutboxItem{{item}}}
	sender := &fakeSender{id: "ABC"}
	svc := newTestWaService(store, sender, true)

	svc.Drain(context.Background())

	if len(sender.calls) != 1 {
		t.Fatalf("calls = %v, want 1", sender.calls)
	}
	if sender.calls[0].phone != "6281234567890" || sender.calls[0].text != "halo" {
		t.Fatalf("call = %#v", sender.calls[0])
	}
	if len(store.sent) != 1 {
		t.Fatalf("sent = %v, want 1", store.sent)
	}
}

func TestWaDrainPermanentError(t *testing.T) {
	item := testItem("0812-3456-7890", "test", "halo", 1, 5)
	store := &fakeWaStore{batches: [][]repo.WaOutboxItem{{item}}}
	sender := &fakeSender{err: &whatsapp.APIError{Status: 400, Body: "bad number"}}
	svc := newTestWaService(store, sender, true)

	svc.Drain(context.Background())

	if len(store.failed) != 1 {
		t.Fatalf("failed = %v, want 1", store.failed)
	}
	if len(store.retried) != 0 {
		t.Fatalf("retried = %v, want none", store.retried)
	}
}

func TestWaDrainRetriesServerError(t *testing.T) {
	item := testItem("0812-3456-7890", "test", "halo", 1, 5)
	store := &fakeWaStore{batches: [][]repo.WaOutboxItem{{item}}}
	sender := &fakeSender{err: &whatsapp.APIError{Status: 500, Body: "boom"}}
	svc := newTestWaService(store, sender, true)
	base := time.Unix(1_700_000_000, 0)
	svc.now = func() time.Time { return base }

	svc.Drain(context.Background())

	if len(store.retried) != 1 {
		t.Fatalf("retried = %v, want 1", store.retried)
	}
	if want := base.Add(time.Minute); !store.retryAt.Equal(want) {
		t.Fatalf("retryAt = %v, want %v", store.retryAt, want)
	}
	if len(store.failed) != 0 {
		t.Fatalf("failed = %v, want none", store.failed)
	}
}

func TestWaDrainGivesUpAtMaxAttempts(t *testing.T) {
	item := testItem("0812-3456-7890", "test", "halo", 5, 5)
	store := &fakeWaStore{batches: [][]repo.WaOutboxItem{{item}}}
	sender := &fakeSender{err: errors.New("timeout")}
	svc := newTestWaService(store, sender, true)

	svc.Drain(context.Background())

	if len(store.failed) != 1 {
		t.Fatalf("failed = %v, want 1", store.failed)
	}
	if len(store.retried) != 0 {
		t.Fatalf("retried = %v, want none", store.retried)
	}
}

func TestWaDrainInvalidPhone(t *testing.T) {
	item := testItem("123", "test", "halo", 1, 5)
	store := &fakeWaStore{batches: [][]repo.WaOutboxItem{{item}}}
	sender := &fakeSender{}
	svc := newTestWaService(store, sender, true)

	svc.Drain(context.Background())

	if len(store.failed) != 1 || len(sender.calls) != 0 {
		t.Fatalf("failed = %v, calls = %v", store.failed, sender.calls)
	}
}

func TestWaDrainUnknownTemplate(t *testing.T) {
	item := testItem("0812-3456-7890", "nope", "", 1, 5)
	store := &fakeWaStore{batches: [][]repo.WaOutboxItem{{item}}}
	svc := newTestWaService(store, &fakeSender{}, true)

	svc.Drain(context.Background())

	if len(store.failed) != 1 {
		t.Fatalf("failed = %v, want 1", store.failed)
	}
}

func TestWaBackoff(t *testing.T) {
	want := []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour, 6 * time.Hour}
	for i, w := range want {
		if got := waBackoff(i + 1); got != w {
			t.Errorf("waBackoff(%d) = %v, want %v", i+1, got, w)
		}
	}
}
