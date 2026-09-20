package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"

	"github.com/google/uuid"
)

type PromoService struct {
	promos *repo.PromoRepo
}

func NewPromoService(promos *repo.PromoRepo) *PromoService {
	return &PromoService{promos: promos}
}

// NormalizePromoCode uppercases and trims codes (stored uppercase).
func NormalizePromoCode(raw string) string {
	return strings.ToUpper(strings.TrimSpace(raw))
}

// PromoQuote is a validated discount preview.
type PromoQuote struct {
	Code     string
	Discount int64
	Net      int64
}

// QuotePromo validates a code against a service price. Empty code returns
// the zero quote without error (no promo applied).
func QuotePromo(ctx context.Context, promos *repo.PromoRepo, rawCode string, priceIDR int64) (PromoQuote, error) {
	code := NormalizePromoCode(rawCode)
	if code == "" {
		return PromoQuote{}, nil
	}
	p, err := promos.ByCode(ctx, code)
	if err != nil {
		return PromoQuote{}, fmt.Errorf("%w: invalid or expired promo code", ErrValidation)
	}
	if !p.Active {
		return PromoQuote{}, fmt.Errorf("%w: invalid or expired promo code", ErrValidation)
	}
	now := time.Now()
	if p.ValidFrom != nil && now.Before(*p.ValidFrom) {
		return PromoQuote{}, fmt.Errorf("%w: promo code is not active yet", ErrValidation)
	}
	if p.ValidTo != nil && now.After(*p.ValidTo) {
		return PromoQuote{}, fmt.Errorf("%w: promo code has expired", ErrValidation)
	}
	if priceIDR < p.MinSpend {
		return PromoQuote{}, fmt.Errorf("%w: promo needs minimum spend %d", ErrValidation, p.MinSpend)
	}
	if p.MaxUses != nil && p.UsedCount >= *p.MaxUses {
		return PromoQuote{}, fmt.Errorf("%w: promo code usage limit reached", ErrValidation)
	}
	var d int64
	if p.Kind == domain.PromoPercent {
		d = priceIDR * int64(p.Value) / 100
	} else {
		d = int64(p.Value)
	}
	if d > priceIDR {
		d = priceIDR
	}
	return PromoQuote{Code: code, Discount: d, Net: priceIDR - d}, nil
}

func (s *PromoService) Quote(ctx context.Context, rawCode string, priceIDR int64) (PromoQuote, error) {
	return QuotePromo(ctx, s.promos, rawCode, priceIDR)
}

func (s *PromoService) ListAll(ctx context.Context) ([]domain.PromoCode, error) {
	return s.promos.ListAll(ctx)
}

// Save validates and stores a code (always uppercased). No edit path:
// codes are immutable; delete and recreate instead.
func (s *PromoService) Save(ctx context.Context, p *domain.PromoCode) error {
	p.Code = NormalizePromoCode(p.Code)
	if p.Code == "" {
		return fmt.Errorf("%w: code required", ErrValidation)
	}
	if p.Kind != domain.PromoPercent && p.Kind != domain.PromoFixed {
		return fmt.Errorf("%w: kind must be percent or fixed", ErrValidation)
	}
	if p.Value <= 0 || (p.Kind == domain.PromoPercent && p.Value > 100) {
		return fmt.Errorf("%w: percent is 1-100, fixed is an IDR amount", ErrValidation)
	}
	if p.MinSpend < 0 {
		return fmt.Errorf("%w: minimum spend must be >= 0", ErrValidation)
	}
	if p.MaxUses != nil && *p.MaxUses <= 0 {
		return fmt.Errorf("%w: max uses must be positive", ErrValidation)
	}
	if p.ValidFrom != nil && p.ValidTo != nil && p.ValidFrom.After(*p.ValidTo) {
		return fmt.Errorf("%w: valid-from is after valid-to", ErrValidation)
	}
	p.Active = true
	return s.promos.Create(ctx, p)
}

func (s *PromoService) ToggleActive(ctx context.Context, id uuid.UUID) error {
	return s.promos.ToggleActive(ctx, id)
}

func (s *PromoService) Delete(ctx context.Context, id uuid.UUID) error {
	return s.promos.Delete(ctx, id)
}
