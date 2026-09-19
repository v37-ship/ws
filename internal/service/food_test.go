package service

import (
	"testing"

	"spa-booking/internal/domain"
)

func TestValidateFoodItem(t *testing.T) {
	price := int64(25000)
	good := &domain.FoodItem{
		Category: domain.FoodCategoryFood, Name: "Nasgor Ayam",
		PriceM: &price,
	}
	if err := ValidateFoodItem(good); err != nil {
		t.Errorf("valid = %v", err)
	}
	badPrice := int64(-100)
	for name, mutate := range map[string]func(*domain.FoodItem){
		"short name": func(f *domain.FoodItem) { f.Name = "A" },
		"bad cat":    func(f *domain.FoodItem) { f.Category = "dessert" },
		"no price":   func(f *domain.FoodItem) { f.PriceM = nil; f.PriceL = nil },
		"neg price":  func(f *domain.FoodItem) { f.PriceM = &badPrice },
		"bad photo":  func(f *domain.FoodItem) { f.ImageURL = "notaurl" },
	} {
		p := int64(25000)
		f := &domain.FoodItem{
			Category: domain.FoodCategoryFood, Name: "Nasgor Ayam",
			PriceM: &p,
		}
		mutate(f)
		if err := ValidateFoodItem(f); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
