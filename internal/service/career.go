package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"

	"github.com/google/uuid"
)

// MaxCVBytes caps CV uploads at 5 MB.
const MaxCVBytes = 5 << 20

var allowedCVExts = map[string]bool{
	".pdf": true, ".doc": true, ".docx": true,
	".jpg": true, ".jpeg": true, ".png": true,
}

type CareerService struct {
	careers *repo.CareerRepo
}

func NewCareerService(careers *repo.CareerRepo) *CareerService {
	return &CareerService{careers: careers}
}

func (s *CareerService) ListJobs(ctx context.Context, activeOnly bool) ([]domain.Job, error) {
	return s.careers.ListJobs(ctx, activeOnly)
}

func (s *CareerService) JobByID(ctx context.Context, id uuid.UUID) (*domain.Job, error) {
	return s.careers.JobByID(ctx, id)
}

func (s *CareerService) CreateJob(ctx context.Context, title string, locationID *uuid.UUID, employmentType domain.EmploymentType, description, requirements string) (*domain.Job, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("%w: job title required", ErrValidation)
	}
	switch employmentType {
	case domain.EmploymentFullTime, domain.EmploymentPartTime, domain.EmploymentContract, domain.EmploymentFreelance:
	default:
		return nil, fmt.Errorf("%w: invalid employment type", ErrValidation)
	}
	j := &domain.Job{
		Title: title, LocationID: locationID, EmploymentType: employmentType,
		Description: strings.TrimSpace(description), Requirements: strings.TrimSpace(requirements),
		Active: true,
	}
	if err := s.careers.SaveJob(ctx, j); err != nil {
		return nil, err
	}
	return j, nil
}

func (s *CareerService) ToggleJobActive(ctx context.Context, id uuid.UUID) error {
	return s.careers.ToggleJobActive(ctx, id)
}

func (s *CareerService) ListApplications(ctx context.Context, jobID uuid.UUID) ([]domain.JobApplication, error) {
	return s.careers.ListApplications(ctx, jobID)
}

func (s *CareerService) ApplicationByID(ctx context.Context, id uuid.UUID) (*domain.JobApplication, error) {
	return s.careers.ApplicationByID(ctx, id)
}

func (s *CareerService) SaveApplication(ctx context.Context, a *domain.JobApplication) error {
	if a.JobID == uuid.Nil || a.FullName == "" || a.Phone == "" {
		return fmt.Errorf("%w: name and phone required", ErrValidation)
	}
	return s.careers.SaveApplication(ctx, a)
}

// ValidateCVName checks the uploaded filename and returns a safe storage name.
func ValidateCVName(original string, size int64) (string, error) {
	if size > MaxCVBytes {
		return "", fmt.Errorf("%w: CV must be under 5 MB", ErrValidation)
	}
	ext := strings.ToLower(filepath.Ext(original))
	if !allowedCVExts[ext] {
		return "", fmt.Errorf("%w: CV must be PDF, DOC, DOCX, JPG or PNG", ErrValidation)
	}
	return uuid.NewString() + ext, nil
}
