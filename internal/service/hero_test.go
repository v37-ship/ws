package service

import (
	"testing"

	"spa-booking/internal/domain"
)

func TestResolveHeroPhoto(t *testing.T) {
	if got := ResolveHeroPhoto(domain.HeroBanner{}); got != DefaultHeroImage {
		t.Errorf("empty = %q want default", got)
	}
	if got := ResolveHeroPhoto(domain.HeroBanner{ImageURL: "https://example.com/a.jpg"}); got != "https://example.com/a.jpg" {
		t.Errorf("url = %q", got)
	}
	if got := ResolveHeroPhoto(domain.HeroBanner{ImagePath: "hero/x.jpg", ImageURL: "https://example.com/a.jpg"}); got != "/uploads/hero/x.jpg" {
		t.Errorf("upload wins = %q", got)
	}
}

func TestHeroSaveValidation(t *testing.T) {
	svc := &HeroService{}
	if err := svc.Save(nil, &domain.HeroBanner{}); err == nil {
		t.Errorf("empty banner: expected error")
	}
}
