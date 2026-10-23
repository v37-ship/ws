package service

import (
	"context"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"

	"github.com/google/uuid"
)

// PaymentSettingsService exposes the single-row admin payment configuration.
type PaymentSettingsService struct {
	repo *repo.PaymentSettingsRepo
}

func NewPaymentSettingsService(r *repo.PaymentSettingsRepo) *PaymentSettingsService {
	return &PaymentSettingsService{repo: r}
}

func (s *PaymentSettingsService) Get(ctx context.Context) (*domain.PaymentSettings, error) {
	return s.repo.Get(ctx)
}

func (s *PaymentSettingsService) SetQRISImage(ctx context.Context, path string, by uuid.UUID) error {
	return s.repo.SetQRISImage(ctx, path, by)
}
