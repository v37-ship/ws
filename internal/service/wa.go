package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"spa-booking/internal/repo"
	"spa-booking/internal/whatsapp"

	"github.com/google/uuid"
)

// WhatsAppSender is the Evolution client surface the worker needs (an
// interface so tests can fake it).
type WhatsAppSender interface {
	SendText(ctx context.Context, phone, text string) (string, error)
}

// waStore is the outbox persistence surface.
type waStore interface {
	ClaimDue(ctx context.Context, limit int) ([]repo.WaOutboxItem, error)
	MarkSent(ctx context.Context, id uuid.UUID) error
	RetryLater(ctx context.Context, id uuid.UUID, nextAt time.Time, errMsg string) error
	MarkFailed(ctx context.Context, id uuid.UUID, errMsg string) error
	RequeueSending(ctx context.Context) (int64, error)
}

// WaService drains the outbound WhatsApp outbox. When enabled is false it
// renders and logs messages, marking them sent (mock mode for dev/CI and for
// running without a paired instance). It never blocks an HTTP request.
type WaService struct {
	store   waStore
	sender  WhatsAppSender
	log     *slog.Logger
	enabled bool
	rate    time.Duration
	now     func() time.Time
}

func NewWaService(store waStore, sender WhatsAppSender, log *slog.Logger, enabled bool, rate time.Duration) *WaService {
	return &WaService{
		store:   store,
		sender:  sender,
		log:     log,
		enabled: enabled,
		rate:    rate,
		now:     time.Now,
	}
}

// RecoverSending resets rows a previous process left mid-send.
func (s *WaService) RecoverSending(ctx context.Context) {
	if n, err := s.store.RequeueSending(ctx); err != nil {
		s.log.Warn("wa outbox requeue", "err", err)
	} else if n > 0 {
		s.log.Info("wa outbox requeued stuck rows", "rows", n)
	}
}

// Drain sends due messages, up to a bounded batch per tick. A failing message
// is rescheduled with backoff (or failed permanently) and never stops the
// rest of the batch.
func (s *WaService) Drain(ctx context.Context) {
	const batches, batchSize = 5, 10
	for i := 0; i < batches; i++ {
		items, err := s.store.ClaimDue(ctx, batchSize)
		if err != nil {
			s.log.Warn("wa outbox claim", "err", err)
			return
		}
		if len(items) == 0 {
			return
		}
		for _, it := range items {
			if s.deliver(ctx, it) && s.enabled && s.rate > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(s.rate):
				}
			}
		}
	}
}

// deliver returns true when a send was attempted (so the caller can pace).
func (s *WaService) deliver(ctx context.Context, it repo.WaOutboxItem) bool {
	phone := whatsapp.NormalizePhone(it.Phone)
	if phone == "" {
		s.fail(ctx, it, "invalid phone number")
		return false
	}
	var payload whatsapp.Payload
	if len(it.Payload) > 0 {
		if err := json.Unmarshal(it.Payload, &payload); err != nil {
			s.fail(ctx, it, "bad payload: "+err.Error())
			return false
		}
	}
	text, err := whatsapp.Render(it.TemplateKey, payload)
	if err != nil {
		s.fail(ctx, it, err.Error())
		return false
	}

	if !s.enabled {
		s.log.Info("wa mock send", "phone", phone, "template", it.TemplateKey, "text", text)
		s.markSent(ctx, it)
		return false
	}
	if s.sender == nil {
		s.retryOrFail(ctx, it, errors.New("whatsapp enabled without sender"))
		return false
	}

	sendCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	id, err := s.sender.SendText(sendCtx, phone, text)
	cancel()
	if err != nil {
		s.log.Warn("wa send failed", "phone", phone, "template", it.TemplateKey,
			"attempt", it.Attempts, "err", err)
		s.retryOrFail(ctx, it, err)
		return false
	}
	s.log.Info("wa sent", "phone", phone, "template", it.TemplateKey, "wa_message_id", id)
	s.markSent(ctx, it)
	return true
}

func (s *WaService) markSent(ctx context.Context, it repo.WaOutboxItem) {
	if err := s.store.MarkSent(ctx, it.ID); err != nil {
		s.log.Warn("wa outbox mark sent", "id", it.ID, "err", err)
	}
}

func (s *WaService) fail(ctx context.Context, it repo.WaOutboxItem, reason string) {
	s.log.Warn("wa message failed", "phone", it.Phone, "template", it.TemplateKey, "reason", reason)
	if err := s.store.MarkFailed(ctx, it.ID, reason); err != nil {
		s.log.Warn("wa outbox mark failed", "id", it.ID, "err", err)
	}
}

func (s *WaService) retryOrFail(ctx context.Context, it repo.WaOutboxItem, cause error) {
	var apiErr *whatsapp.APIError
	if errors.As(cause, &apiErr) && apiErr.Permanent() {
		s.fail(ctx, it, cause.Error())
		return
	}
	if it.Attempts >= it.MaxAttempts {
		s.fail(ctx, it, fmt.Sprintf("gave up after %d attempts: %v", it.Attempts, cause))
		return
	}
	next := s.now().Add(waBackoff(it.Attempts))
	if err := s.store.RetryLater(ctx, it.ID, next, cause.Error()); err != nil {
		s.log.Warn("wa outbox retry", "id", it.ID, "err", err)
		return
	}
	s.log.Info("wa message rescheduled", "template", it.TemplateKey, "attempt", it.Attempts, "next", next)
}

// waBackoff is the delay before the next attempt, indexed by the attempt that
// just failed: 1m, 5m, 15m, 1h, 6h.
func waBackoff(attempt int) time.Duration {
	switch attempt {
	case 1:
		return time.Minute
	case 2:
		return 5 * time.Minute
	case 3:
		return 15 * time.Minute
	case 4:
		return time.Hour
	default:
		return 6 * time.Hour
	}
}
