package service

import (
	"context"
	"errors"
	"testing"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
)

type fakeWaSettingsStore struct {
	row       *domain.WaSettings
	overrides map[string]string
}

func (f *fakeWaSettingsStore) Get(context.Context) (*domain.WaSettings, error) {
	if f.row == nil {
		return &domain.WaSettings{}, nil
	}
	return f.row, nil
}

func (f *fakeWaSettingsStore) Save(_ context.Context, s domain.WaSettings, _ uuid.UUID) error {
	f.row = &s
	return nil
}

func (f *fakeWaSettingsStore) ListOverrides(context.Context) (map[string]string, error) {
	if f.overrides == nil {
		return map[string]string{}, nil
	}
	return f.overrides, nil
}

func (f *fakeWaSettingsStore) SetOverride(_ context.Context, k, b string, _ uuid.UUID) error {
	if f.overrides == nil {
		f.overrides = map[string]string{}
	}
	f.overrides[k] = b
	return nil
}

func (f *fakeWaSettingsStore) DeleteOverride(_ context.Context, k string) error {
	delete(f.overrides, k)
	return nil
}

// newTestWaSettings builds a settings service whose env defaults are cfg and
// whose stored row is empty (so Effective returns cfg).
func newTestWaSettings(cfg WaConfig) *WaSettingsService {
	return NewWaSettingsService(&fakeWaSettingsStore{row: &domain.WaSettings{}}, cfg, testLogger())
}

func boolPtr(b bool) *bool    { return &b }
func intPtr(n int) *int       { return &n }
func strPtr(s string) *string { return &s }

func TestWaSettingsEffective(t *testing.T) {
	ctx := context.Background()
	defaults := WaConfig{
		CustomerEnabled: true, ReminderEnabled: true, RemindHour: 18,
		ReviewEnabled: true, ReviewURL: "https://env.example/review",
		GroupMode: "web", StaffGroupJID: "111@g.us",
		DigestEnabled: true, DigestHour: 7, OutboundEnabled: true,
	}
	store := &fakeWaSettingsStore{row: &domain.WaSettings{}}
	svc := NewWaSettingsService(store, defaults, testLogger())

	if got := svc.Effective(ctx); got != defaults {
		t.Errorf("empty row must fall back to env: %+v", got)
	}

	store.row = &domain.WaSettings{
		RemindHour:      intPtr(9),
		GroupMode:       strPtr("off"),
		OutboundEnabled: boolPtr(false),
	}
	svc.Invalidate()
	got := svc.Effective(ctx)
	if got.RemindHour != 9 || got.GroupMode != "off" || got.OutboundEnabled {
		t.Errorf("stored fields must win: %+v", got)
	}
	if !got.CustomerEnabled || got.StaffGroupJID != "111@g.us" {
		t.Errorf("unset fields must keep the env default: %+v", got)
	}
}

func TestWaSettingsCacheInvalidatedOnSave(t *testing.T) {
	ctx := context.Background()
	store := &fakeWaSettingsStore{row: &domain.WaSettings{}}
	svc := NewWaSettingsService(store, WaConfig{RemindHour: 18, ReminderEnabled: true}, testLogger())
	_ = svc.Effective(ctx)

	if err := svc.Save(ctx, domain.WaSettings{RemindHour: intPtr(7)}, uuid.New()); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got := svc.Effective(ctx); got.RemindHour != 7 {
		t.Errorf("save must invalidate the cache, got hour %d", got.RemindHour)
	}
}

func TestWaSettingsValidation(t *testing.T) {
	ctx := context.Background()
	svc := NewWaSettingsService(&fakeWaSettingsStore{}, WaConfig{}, testLogger())
	cases := []struct {
		name string
		row  domain.WaSettings
	}{
		{"hour too high", domain.WaSettings{RemindHour: intPtr(24)}},
		{"hour negative", domain.WaSettings{DigestHour: intPtr(-1)}},
		{"bad group mode", domain.WaSettings{GroupMode: strPtr("sometimes")}},
		{"bad jid", domain.WaSettings{StaffGroupJID: strPtr("12345")}},
		{"bad url", domain.WaSettings{ReviewURL: strPtr("g.page/x")}},
	}
	for _, tc := range cases {
		if err := svc.Save(ctx, tc.row, uuid.New()); !errors.Is(err, ErrValidation) {
			t.Errorf("%s: want ErrValidation, got %v", tc.name, err)
		}
	}
	valid := domain.WaSettings{
		RemindHour: intPtr(0), DigestHour: intPtr(23),
		GroupMode: strPtr("all"), StaffGroupJID: strPtr("120363-429@g.us"),
		ReviewURL: strPtr("https://g.page/r/x/review"),
	}
	if err := svc.Save(ctx, valid, uuid.New()); err != nil {
		t.Errorf("valid settings rejected: %v", err)
	}
}

func TestWaSettingsOverrideValidation(t *testing.T) {
	ctx := context.Background()
	svc := NewWaSettingsService(&fakeWaSettingsStore{}, WaConfig{}, testLogger())
	if err := svc.SetOverride(ctx, "booking_remind", "Halo {name}, jumpa {when}!", uuid.New()); err != nil {
		t.Errorf("valid override rejected: %v", err)
	}
	if err := svc.SetOverride(ctx, "booking_remind", "Halo {nmae}", uuid.New()); !errors.Is(err, ErrValidation) {
		t.Errorf("unknown placeholder must fail, got %v", err)
	}
	if err := svc.SetOverride(ctx, "staff_digest", "nope", uuid.New()); !errors.Is(err, ErrValidation) {
		t.Errorf("staff templates are not editable, got %v", err)
	}
	if err := svc.SetOverride(ctx, "booking_remind", "   ", uuid.New()); !errors.Is(err, ErrValidation) {
		t.Errorf("empty override must fail, got %v", err)
	}
}

func TestWaSettingsResetAll(t *testing.T) {
	ctx := context.Background()
	store := &fakeWaSettingsStore{row: &domain.WaSettings{RemindHour: intPtr(9)}}
	svc := NewWaSettingsService(store, WaConfig{RemindHour: 18}, testLogger())
	if err := svc.ResetAll(ctx, uuid.New()); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if got := svc.Effective(ctx); got.RemindHour != 18 {
		t.Errorf("reset must restore the env default, got %d", got.RemindHour)
	}
}
