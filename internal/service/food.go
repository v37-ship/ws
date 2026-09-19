package service

import (
	"context"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"

	"github.com/google/uuid"
)

type FoodService struct {
	repo *repo.FoodRepo
}

func NewFoodService(r *repo.FoodRepo) *FoodService { return &FoodService{repo: r} }

func (s *FoodService) ListActive(ctx context.Context) ([]domain.FoodItem, error) {
	return s.repo.ListActive(ctx)
}

func (s *FoodService) ListAll(ctx context.Context) ([]domain.FoodItem, error) {
	return s.repo.ListAll(ctx)
}

func (s *FoodService) ByID(ctx context.Context, id uuid.UUID) (*domain.FoodItem, error) {
	return s.repo.ByID(ctx, id)
}

// Save validates and inserts or updates a menu row. Error strings stay
// English like the other service .Error messages.
func (s *FoodService) Save(ctx context.Context, f *domain.FoodItem) error {
	if err := ValidateFoodItem(f); err != nil {
		return err
	}
	if f.ID == uuid.Nil {
		f.Active = true
		return s.repo.Create(ctx, f)
	}
	return s.repo.Update(ctx, f)
}

func (s *FoodService) ToggleActive(ctx context.Context, id uuid.UUID) error {
	return s.repo.ToggleActive(ctx, id)
}

func (s *FoodService) Delete(ctx context.Context, id uuid.UUID) error {
	return s.repo.Delete(ctx, id)
}

func ValidateFoodItem(f *domain.FoodItem) error {
	if f.Category != domain.FoodCategoryFood && f.Category != domain.FoodCategoryBeverage {
		return fmt.Errorf("%w: category must be food or beverage", ErrValidation)
	}
	name := strings.TrimSpace(f.Name)
	if utf8.RuneCountInString(name) < 2 || utf8.RuneCountInString(name) > 100 {
		return fmt.Errorf("%w: name must be 2-100 characters", ErrValidation)
	}
	f.Name = name
	f.Description = strings.TrimSpace(f.Description)
	if f.PriceM == nil && f.PriceL == nil {
		return fmt.Errorf("%w: at least one price required", ErrValidation)
	}
	for _, p := range []*int64{f.PriceM, f.PriceL} {
		if p != nil && *p < 0 {
			return fmt.Errorf("%w: price must be >= 0", ErrValidation)
		}
	}
	f.Variants = strings.TrimSpace(f.Variants)
	f.ImagePath = strings.TrimSpace(f.ImagePath)
	if err := ValidateImageURL(f.ImageURL); err != nil {
		return err
	}
	f.ImageURL = strings.TrimSpace(f.ImageURL)
	return nil
}

// ResolveFoodPhoto picks the display picture: local upload first, then
// internet link, then "" (templates skip the img).
func ResolveFoodPhoto(f domain.FoodItem) string {
	if f.ImagePath != "" {
		return "/uploads/food/" + path.Base(f.ImagePath)
	}
	return strings.TrimSpace(f.ImageURL)
}

func idr(v int64) *int64 { return &v }

// SeedFood inserts the Bu Nah launch menu once, in menu-card order.
func (s *FoodService) SeedFood(ctx context.Context) error {
	n, err := s.repo.Count(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	m25, l32 := idr(25000), idr(32000)
	foods := []struct {
		name string
		pm   *int64
		pl   *int64
	}{
		{"Nasgor Ayam", m25, l32},
		{"Nasgor Seafood", m25, l32},
		{"Bakmi Goreng Ayam", m25, l32},
		{"Bakmi Goreng Seafood", m25, l32},
		{"Cap Cay Ayam", m25, l32},
		{"Cap Cay Seafood", m25, l32},
		{"Omelet Sayur/Fuyunghai", m25, l32},
		{"Kwe tiauw Ayam", m25, l32},
		{"Kwe tiauw Seafood", m25, l32},
		{"Chicken Butter Rice", m25, l32},
		{"Sapo Tahu", nil, l32},
		{"Sop Ayam", m25, l32},
	}
	for i, fd := range foods {
		if err := s.repo.Create(ctx, &domain.FoodItem{
			Category: domain.FoodCategoryFood, Name: fd.name,
			PriceM: fd.pm, PriceL: fd.pl, Active: true, Sort: i + 1,
		}); err != nil {
			return err
		}
	}
	beverages := []domain.FoodItem{
		{Category: domain.FoodCategoryBeverage, Name: "Jamu BK/KA 350ml", PriceM: idr(15000), PreOrder: true, Active: true, Sort: 1},
		{Category: domain.FoodCategoryBeverage, Name: "Jamu BK/KA 1 liter", PriceM: idr(30000), PreOrder: true, Active: true, Sort: 2},
		{Category: domain.FoodCategoryBeverage, Name: "Teh/es teh", PriceM: idr(5000), Active: true, Sort: 3},
		{Category: domain.FoodCategoryBeverage, Name: "Jeruk/es jeruk", PriceM: idr(10000), Active: true, Sort: 4},
		{Category: domain.FoodCategoryBeverage, Name: "Puding", PriceM: idr(10000), Variants: "coklat, stroberi, mangga, hazelnut, alpukat, caramel", PreOrder: true, Active: true, Sort: 5},
		{Category: domain.FoodCategoryBeverage, Name: "Snack", PriceM: idr(25000), Variants: "Ceker, Usus, Peyek, Kacang", PreOrder: true, Active: true, Sort: 6},
	}
	for i := range beverages {
		if err := s.repo.Create(ctx, &beverages[i]); err != nil {
			return err
		}
	}
	return nil
}
