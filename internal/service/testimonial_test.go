package service

import (
	"testing"

	"spa-booking/internal/domain"
)

func TestValidateTestimonial(t *testing.T) {
	good := &domain.Testimonial{
		Name: "Sari", Age: 28, Rating: 5,
		PhotoURL: "https://randomuser.me/api/portraits/women/44.jpg",
		Message:  "Pijat pemulihan di Wangga bikin badanku jauh lebih ringan.",
	}
	if err := ValidateTestimonial(good); err != nil {
		t.Errorf("valid = %v", err)
	}
	for name, mutate := range map[string]func(*domain.Testimonial){
		"short name":  func(tm *domain.Testimonial) { tm.Name = "A" },
		"bad rating":  func(tm *domain.Testimonial) { tm.Rating = 6 },
		"zero rating": func(tm *domain.Testimonial) { tm.Rating = 0 },
		"short msg":   func(tm *domain.Testimonial) { tm.Message = "bagus" },
		"bad age":     func(tm *domain.Testimonial) { tm.Age = 5 },
		"bad photo":   func(tm *domain.Testimonial) { tm.PhotoURL = "notaurl" },
	} {
		tm := &domain.Testimonial{
			Name: "Sari", Age: 28, Rating: 5,
			PhotoURL: "https://randomuser.me/api/portraits/women/44.jpg",
			Message:  "Pijat pemulihan di Wangga bikin badanku jauh lebih ringan.",
		}
		mutate(tm)
		if err := ValidateTestimonial(tm); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestTestimonialInitials(t *testing.T) {
	cases := map[string]string{
		"Sari":         "S",
		"Dewi Lestari": "DL",
		"":             "W",
	}
	for name, want := range cases {
		if got := (domain.Testimonial{Name: name}).Initials(); got != want {
			t.Errorf("%q initials = %q want %q", name, got, want)
		}
	}
}
