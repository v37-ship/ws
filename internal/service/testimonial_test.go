package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
)

// fakeTestimonialStore records Create so the submit rules run without a
// database (same shape as the waStore fake).
type fakeTestimonialStore struct{ created []domain.Testimonial }

func (f *fakeTestimonialStore) Create(_ context.Context, t *domain.Testimonial) error {
	f.created = append(f.created, *t)
	return nil
}

func (f *fakeTestimonialStore) ListApproved(context.Context, int) ([]domain.Testimonial, error) {
	return nil, nil
}

func (f *fakeTestimonialStore) ListAll(context.Context) ([]domain.Testimonial, error) {
	return nil, nil
}

func (f *fakeTestimonialStore) SetStatus(context.Context, uuid.UUID, domain.TestimonialStatus) error {
	return nil
}

func (f *fakeTestimonialStore) Delete(context.Context, uuid.UUID) error { return nil }

func (f *fakeTestimonialStore) Count(context.Context) (int, error) { return 0, nil }

func (f *fakeTestimonialStore) TouchCreatedAt(context.Context, uuid.UUID, int) error { return nil }

// TestSubmitGuestLandsPending pins the counter QR card flow: a review with no
// customer account is accepted and always waits for moderation.
func TestSubmitGuestLandsPending(t *testing.T) {
	st := &fakeTestimonialStore{}
	svc := &TestimonialService{repo: st}
	in := &domain.Testimonial{
		Name: "Sari", Rating: 5,
		Message: "Bidannya sabat banget, ruangan tenang dan bersih sekali.",
	}
	if err := svc.Submit(context.Background(), in); err != nil {
		t.Fatalf("guest submit = %v", err)
	}
	if in.CustomerID != nil {
		t.Errorf("guest CustomerID = %v, want nil", in.CustomerID)
	}
	if in.Status != domain.TestimonialPending {
		t.Errorf("guest status = %q, want %q", in.Status, domain.TestimonialPending)
	}
	if len(st.created) != 1 {
		t.Fatalf("Create calls = %d, want 1", len(st.created))
	}
	if got := st.created[0]; got.Status != domain.TestimonialPending || got.CustomerID != nil {
		t.Errorf("stored %+v, want pending with nil CustomerID", got)
	}
}

// TestSubmitGuestNameRequired pins that a guest cannot submit without typing a
// name (there is no account to fall back to).
func TestSubmitGuestNameRequired(t *testing.T) {
	st := &fakeTestimonialStore{}
	svc := &TestimonialService{repo: st}
	err := svc.Submit(context.Background(), &domain.Testimonial{
		Name: "S", Rating: 5,
		Message: "Bidannya sabat banget, ruangan tenang dan bersih sekali.",
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("short guest name err = %v, want ErrValidation", err)
	}
	if !strings.Contains(err.Error(), "name must be 2-80 characters") {
		t.Errorf("err = %q, want name message", err)
	}
	if len(st.created) != 0 {
		t.Errorf("Create calls = %d, want 0 on invalid input", len(st.created))
	}
}

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
