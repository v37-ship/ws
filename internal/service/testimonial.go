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

type TestimonialService struct {
	repo *repo.TestimonialRepo
}

func NewTestimonialService(r *repo.TestimonialRepo) *TestimonialService {
	return &TestimonialService{repo: r}
}

func (s *TestimonialService) ListApproved(ctx context.Context, limit int) ([]domain.Testimonial, error) {
	return s.repo.ListApproved(ctx, limit)
}

func (s *TestimonialService) ListAll(ctx context.Context) ([]domain.Testimonial, error) {
	return s.repo.ListAll(ctx)
}

// Submit validates a customer entry, forces pending moderation, and saves it.
func (s *TestimonialService) Submit(ctx context.Context, t *domain.Testimonial) error {
	t.Status = domain.TestimonialPending
	if err := ValidateTestimonial(t); err != nil {
		return err
	}
	return s.repo.Create(ctx, t)
}

// Save validates and stores an admin-managed entry (status set by caller).
func (s *TestimonialService) Save(ctx context.Context, t *domain.Testimonial) error {
	if t.Status == "" {
		t.Status = domain.TestimonialApproved
	}
	if err := ValidateTestimonial(t); err != nil {
		return err
	}
	return s.repo.Create(ctx, t)
}

func (s *TestimonialService) SetStatus(ctx context.Context, id uuid.UUID, status domain.TestimonialStatus) error {
	switch status {
	case domain.TestimonialPending, domain.TestimonialApproved, domain.TestimonialHidden:
		return s.repo.SetStatus(ctx, id, status)
	default:
		return fmt.Errorf("%w: unknown status", ErrValidation)
	}
}

func (s *TestimonialService) Delete(ctx context.Context, id uuid.UUID) error {
	return s.repo.Delete(ctx, id)
}

// ValidateTestimonial checks customer/admin input. Error strings stay
// English like the other service .Error messages.
func ValidateTestimonial(t *domain.Testimonial) error {
	name := strings.TrimSpace(t.Name)
	if utf8.RuneCountInString(name) < 2 || utf8.RuneCountInString(name) > 80 {
		return fmt.Errorf("%w: name must be 2-80 characters", ErrValidation)
	}
	t.Name = name
	if t.Rating < 1 || t.Rating > 5 {
		return fmt.Errorf("%w: rating must be 1-5 stars", ErrValidation)
	}
	if t.Age < 0 || t.Age > 100 || (t.Age > 0 && t.Age < 10) {
		return fmt.Errorf("%w: age looks wrong", ErrValidation)
	}
	msg := strings.TrimSpace(t.Message)
	if n := utf8.RuneCountInString(msg); n < 10 || n > 1000 {
		return fmt.Errorf("%w: message must be 10-1000 characters", ErrValidation)
	}
	t.Message = msg
	t.PhotoPath = strings.TrimSpace(t.PhotoPath)
	if err := ValidateImageURL(t.PhotoURL); err != nil {
		return err
	}
	t.PhotoURL = strings.TrimSpace(t.PhotoURL)
	return nil
}

// ResolveTestimonialPhoto picks the display picture: local upload first,
// then internet link, then "" (templates fall back to initials avatars).
func ResolveTestimonialPhoto(t domain.Testimonial) string {
	if t.PhotoPath != "" {
		return "/uploads/testimonials/" + path.Base(t.PhotoPath)
	}
	return strings.TrimSpace(t.PhotoURL)
}

// SeedTestimonials inserts the launch carousel entries once: approved,
// customer-less, newest-first by staggered timestamps.
func (s *TestimonialService) SeedTestimonials(ctx context.Context) error {
	n, err := s.repo.Count(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	seeds := []domain.Testimonial{
		{Name: "Sari", Age: 28, Rating: 5, PhotoURL: "https://randomuser.me/api/portraits/women/44.jpg",
			Message: "Baby Elvano (8 bulan) pertama kali main air hangat di Wangga dan langsung ketawa-ketawa. Bidannya sabar banget, ruangan tenang dan bersih. Pasti balik lagi bulan depan!"},
		{Name: "Dewi", Age: 34, Rating: 5, PhotoURL: "https://randomuser.me/api/portraits/women/65.jpg",
			Message: "Dua minggu setelah melahirkan badanku remuk semua. Pijat pemulihan di Wangga bikin punggung dan pinggang jauh lebih ringan. Tidur malam jadi nyenyak lagi. Terima kasih banyak!"},
		{Name: "Putri", Age: 26, Rating: 5, PhotoURL: "https://randomuser.me/api/portraits/women/21.jpg",
			Message: "Pesan homecare karena bayiku baru 3 minggu dan aku belum berani keluar rumah. Bidannya datang tepat waktu dan bawa semua peralatan sendiri. Praktis dan sangat membantu untuk ibu baru sepertiku."},
		{Name: "Maya", Age: 31, Rating: 5, PhotoURL: "https://randomuser.me/api/portraits/women/50.jpg",
			Message: "Facialnya lembut dan tidak perih sama sekali, padahal kulitku sensitif. Lanjut pijat bunda 60 menit, pulang-pulang badan enteng. Tempatnya wangi dan adem, betah banget."},
		{Name: "Ratna", Age: 47, Rating: 4, PhotoURL: "https://randomuser.me/api/portraits/women/68.jpg",
			Message: "Sudah coba pijat di banyak tempat, di sini tekanannya paling pas untuk sakit punggungku. Terapisnya ramah dan sopan. Bintang 4 karena parkirnya agak susah pas jam ramai."},
		{Name: "Fitri", Age: 29, Rating: 5, PhotoURL: "https://randomuser.me/api/portraits/women/33.jpg",
			Message: "Anakku (5 bulan) yang biasanya rewel kalau mandi, di sini malah anteng dan senyum-senyum. Airnya hangat, musiknya tenang. Pengalaman pertama yang tidak akan kami lupakan."},
		{Name: "Ayu", Age: 24, Rating: 5, PhotoURL: "https://randomuser.me/api/portraits/women/17.jpg",
			Message: "Hamil 7 bulan pinggang rasanya mau copot. Setelah pijat kehamilan di sini, duduk dan jalan jauh lebih nyaman. Bidannya paham posisi aman untuk ibu hamil, jadi aku tenang."},
		{Name: "Rina", Age: 41, Rating: 5, PhotoURL: "https://randomuser.me/api/portraits/women/56.jpg",
			Message: "Ambil paket toddler + mom massage bareng anakku (3 tahun). Anakku ditangani dengan sabar luar biasa sementara aku akhirnya bisa relaksasi. Worth every rupiah!"},
		{Name: "Intan", Age: 36, Rating: 4, PhotoURL: "https://randomuser.me/api/portraits/women/26.jpg",
			Message: "Booking online-nya gampang, bayar QRIS langsung beres. Cuma slot Sabtu sore cepat penuh, jadi harus booking dari jauh hari. Pelayanannya sendiri memuaskan."},
		{Name: "Wulan", Age: 52, Rating: 5, PhotoURL: "https://randomuser.me/api/portraits/women/62.jpg",
			Message: "Saya antar cucu (1 tahun) baby spa di sini. Sebagai nenek saya cerewet soal kebersihan, dan tempat ini lolos standar saya. Air diganti, handuk bersih, stafnya sayang anak."},
	}
	for i := range seeds {
		seeds[i].Status = domain.TestimonialApproved
		if err := s.repo.Create(ctx, &seeds[i]); err != nil {
			return err
		}
		if err := s.repo.SetStatus(ctx, seeds[i].ID, domain.TestimonialApproved); err != nil {
			return err
		}
		if err := s.repo.TouchCreatedAt(ctx, seeds[i].ID, i); err != nil {
			return err
		}
	}
	return nil
}
