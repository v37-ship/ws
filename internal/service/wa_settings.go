package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/whatsapp"

	"github.com/google/uuid"
)

// WaConfig is the fully resolved WhatsApp configuration (no pointers): the DB
// value where an admin set one, the env default otherwise.
type WaConfig struct {
	CustomerEnabled bool
	ReminderEnabled bool
	RemindHour      int // WIB hour of the H-1 reminder
	ReviewEnabled   bool
	ReviewURL       string
	GroupMode       string // off | web | all
	StaffGroupJID   string
	DigestEnabled   bool
	DigestHour      int // WIB hour of the daily agenda
	OutboundEnabled bool
}

// waSettingsStore is the persistence surface (an interface so tests can fake).
type waSettingsStore interface {
	Get(ctx context.Context) (*domain.WaSettings, error)
	Save(ctx context.Context, s domain.WaSettings, by uuid.UUID) error
	ListOverrides(ctx context.Context) (map[string]string, error)
	SetOverride(ctx context.Context, key, body string, by uuid.UUID) error
	DeleteOverride(ctx context.Context, key string) error
}

// WaSettingsService resolves the effective WhatsApp configuration. It caches
// the row for a short TTL and keeps the last good value if a read fails, so a
// transient DB blip can never flip reminders back on.
type WaSettingsService struct {
	store    waSettingsStore
	defaults WaConfig
	log      *slog.Logger
	ttl      time.Duration

	mu       sync.RWMutex
	cached   *WaConfig
	cachedAt time.Time
}

func NewWaSettingsService(store waSettingsStore, defaults WaConfig, log *slog.Logger) *WaSettingsService {
	return &WaSettingsService{store: store, defaults: defaults, log: log, ttl: 15 * time.Second}
}

// Effective returns the resolved configuration (never fails).
func (s *WaSettingsService) Effective(ctx context.Context) WaConfig {
	s.mu.RLock()
	cached, at := s.cached, s.cachedAt
	s.mu.RUnlock()
	if cached != nil && time.Since(at) < s.ttl {
		return *cached
	}

	row, err := s.store.Get(ctx)
	if err != nil {
		if s.log != nil {
			s.log.Warn("wa settings read", "err", err)
		}
		if cached != nil {
			return *cached
		}
		return s.defaults
	}
	cfg := s.defaults
	applyWaSettings(row, &cfg)
	s.mu.Lock()
	s.cached = &cfg
	s.cachedAt = time.Now()
	s.mu.Unlock()
	return cfg
}

// Defaults returns the env-derived defaults (the "from env" side of the UI).
func (s *WaSettingsService) Defaults() WaConfig { return s.defaults }

// Raw returns the stored row (which fields an admin has customised).
func (s *WaSettingsService) Raw(ctx context.Context) (*domain.WaSettings, error) {
	return s.store.Get(ctx)
}

// Invalidate drops the cache so the next read sees a just-saved value.
func (s *WaSettingsService) Invalidate() {
	s.mu.Lock()
	s.cached = nil
	s.mu.Unlock()
}

// Save validates and writes the whole row (nil fields fall back to env).
func (s *WaSettingsService) Save(ctx context.Context, row domain.WaSettings, by uuid.UUID) error {
	if err := validateWaSettings(row); err != nil {
		return err
	}
	if err := s.store.Save(ctx, row, by); err != nil {
		return err
	}
	s.Invalidate()
	return nil
}

// ResetAll clears every field back to the env default.
func (s *WaSettingsService) ResetAll(ctx context.Context, by uuid.UUID) error {
	if err := s.store.Save(ctx, domain.WaSettings{}, by); err != nil {
		return err
	}
	s.Invalidate()
	return nil
}

// Overrides returns the copy overrides keyed by template.
func (s *WaSettingsService) Overrides(ctx context.Context) (map[string]string, error) {
	return s.store.ListOverrides(ctx)
}

// Override returns the stored body for a template, or ok=false when the
// built-in copy should be used.
func (s *WaSettingsService) Override(ctx context.Context, key string) (string, bool) {
	all, err := s.store.ListOverrides(ctx)
	if err != nil {
		return "", false
	}
	body, ok := all[key]
	return body, ok && strings.TrimSpace(body) != ""
}

// SetOverride stores an admin copy override after validating it.
func (s *WaSettingsService) SetOverride(ctx context.Context, key, body string, by uuid.UUID) error {
	if err := whatsapp.ValidateOverride(key, body); err != nil {
		return fmt.Errorf("%w: %s", ErrValidation, err.Error())
	}
	return s.store.SetOverride(ctx, key, strings.TrimSpace(body), by)
}

// ResetOverride deletes an override so the built-in copy returns.
func (s *WaSettingsService) ResetOverride(ctx context.Context, key string, by uuid.UUID) error {
	return s.store.DeleteOverride(ctx, key)
}

// applyWaSettings overlays the non-nil stored fields onto the defaults.
func applyWaSettings(row *domain.WaSettings, cfg *WaConfig) {
	if row == nil {
		return
	}
	if row.CustomerEnabled != nil {
		cfg.CustomerEnabled = *row.CustomerEnabled
	}
	if row.ReminderEnabled != nil {
		cfg.ReminderEnabled = *row.ReminderEnabled
	}
	if row.RemindHour != nil {
		cfg.RemindHour = *row.RemindHour
	}
	if row.ReviewEnabled != nil {
		cfg.ReviewEnabled = *row.ReviewEnabled
	}
	if row.ReviewURL != nil && *row.ReviewURL != "" {
		cfg.ReviewURL = *row.ReviewURL
	}
	if row.GroupMode != nil && *row.GroupMode != "" {
		cfg.GroupMode = *row.GroupMode
	}
	if row.StaffGroupJID != nil && *row.StaffGroupJID != "" {
		cfg.StaffGroupJID = *row.StaffGroupJID
	}
	if row.DigestEnabled != nil {
		cfg.DigestEnabled = *row.DigestEnabled
	}
	if row.DigestHour != nil {
		cfg.DigestHour = *row.DigestHour
	}
	if row.OutboundEnabled != nil {
		cfg.OutboundEnabled = *row.OutboundEnabled
	}
}

// validateWaSettings checks only the fields an admin actually set.
func validateWaSettings(row domain.WaSettings) error {
	if row.RemindHour != nil && (*row.RemindHour < 0 || *row.RemindHour > 23) {
		return fmt.Errorf("%w: reminder hour must be 0-23", ErrValidation)
	}
	if row.DigestHour != nil && (*row.DigestHour < 0 || *row.DigestHour > 23) {
		return fmt.Errorf("%w: digest hour must be 0-23", ErrValidation)
	}
	if row.GroupMode != nil {
		switch *row.GroupMode {
		case "", "off", "web", "all":
		default:
			return fmt.Errorf("%w: group mode must be off, web or all", ErrValidation)
		}
	}
	if row.StaffGroupJID != nil && *row.StaffGroupJID != "" && !validGroupJID(*row.StaffGroupJID) {
		return fmt.Errorf("%w: group id must look like 1234-567@g.us", ErrValidation)
	}
	if row.ReviewURL != nil && *row.ReviewURL != "" && !validHTTPURL(*row.ReviewURL) {
		return fmt.Errorf("%w: review link must start with http:// or https://", ErrValidation)
	}
	return nil
}

// validGroupJID accepts the Evolution group JID form (digits/dashes @g.us).
func validGroupJID(jid string) bool {
	jid = strings.TrimSpace(jid)
	if !strings.HasSuffix(jid, "@g.us") {
		return false
	}
	local := strings.TrimSuffix(jid, "@g.us")
	if local == "" {
		return false
	}
	for _, r := range local {
		if (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

func validHTTPURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	return strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://")
}
