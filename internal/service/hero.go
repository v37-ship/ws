package service

import (
	"context"
	"fmt"
	"path"
	"strings"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"

	"github.com/google/uuid"
)

// DefaultHeroImage is the built-in banner while no staff banners exist.
const DefaultHeroImage = "https://images.unsplash.com/photo-1515488042361-ee00e0ddd4e4?auto=format&fit=crop&w=1800&q=80"

type HeroService struct {
	repo *repo.HeroRepo
}

func NewHeroService(r *repo.HeroRepo) *HeroService { return &HeroService{repo: r} }

func (s *HeroService) ListActive(ctx context.Context) ([]domain.HeroBanner, error) {
	return s.repo.ListActive(ctx)
}

func (s *HeroService) ListAll(ctx context.Context) ([]domain.HeroBanner, error) {
	return s.repo.ListAll(ctx)
}

// Save validates and stores a banner (upload path or link required).
func (s *HeroService) Save(ctx context.Context, h *domain.HeroBanner) error {
	h.ImagePath = strings.TrimSpace(h.ImagePath)
	if err := ValidateImageURL(h.ImageURL); err != nil {
		return err
	}
	h.ImageURL = strings.TrimSpace(h.ImageURL)
	if h.ImagePath == "" && h.ImageURL == "" {
		return fmt.Errorf("%w: upload or link a photo", ErrValidation)
	}
	if h.ID == uuid.Nil {
		h.Active = true
		if sort, err := s.repo.NextSort(ctx); err == nil {
			h.Sort = sort
		}
		return s.repo.Create(ctx, h)
	}
	return fmt.Errorf("%w: banners are add/remove only", ErrValidation)
}

func (s *HeroService) SetSort(ctx context.Context, id uuid.UUID, sort int) error {
	return s.repo.SetSort(ctx, id, sort)
}

func (s *HeroService) ToggleActive(ctx context.Context, id uuid.UUID) error {
	return s.repo.ToggleActive(ctx, id)
}

func (s *HeroService) Delete(ctx context.Context, id uuid.UUID) error {
	return s.repo.Delete(ctx, id)
}

// ResolveHeroPhoto picks the display picture: local upload first, then
// internet link, then the built-in default.
func ResolveHeroPhoto(h domain.HeroBanner) string {
	if h.ImagePath != "" {
		return "/uploads/hero/" + path.Base(h.ImagePath)
	}
	if strings.TrimSpace(h.ImageURL) != "" {
		return strings.TrimSpace(h.ImageURL)
	}
	return DefaultHeroImage
}
