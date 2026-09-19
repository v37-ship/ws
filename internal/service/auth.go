package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"spa-booking/internal/auth"
	"spa-booking/internal/domain"
	"spa-booking/internal/repo"

	"github.com/google/uuid"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrAccountDeactivated = errors.New("account is deactivated")
	ErrEmailTaken         = errors.New("email already registered")
	ErrPhoneTaken         = errors.New("phone already registered")
	ErrUnauthorized       = errors.New("unauthorized")
	ErrForbidden          = errors.New("forbidden")
	ErrValidation         = errors.New("validation error")
	ErrConflict           = errors.New("booking conflict")
)

type AuthService struct {
	users    *repo.UserRepo
	sessions *repo.SessionRepo
	ttl      time.Duration
}

func NewAuthService(users *repo.UserRepo, sessions *repo.SessionRepo, ttl time.Duration) *AuthService {
	return &AuthService{users: users, sessions: sessions, ttl: ttl}
}

func (s *AuthService) Register(ctx context.Context, email, password, fullName, phone string) (*domain.User, *domain.Session, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	fullName = strings.TrimSpace(fullName)
	phone = strings.TrimSpace(phone)
	if email == "" || len(password) < 8 || fullName == "" {
		return nil, nil, fmt.Errorf("%w: email, name, and password (min 8) required", ErrValidation)
	}
	if _, err := s.users.ByEmail(ctx, email); err == nil {
		return nil, nil, ErrEmailTaken
	} else if !errors.Is(err, repo.ErrNotFound) {
		return nil, nil, err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, nil, err
	}
	u := &domain.User{
		Email:        email,
		PasswordHash: hash,
		FullName:     fullName,
		Phone:        phone,
		Role:         domain.RoleCustomer,
		IsActive:     true,
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, nil, err
	}
	sess, err := s.createSession(ctx, u.ID)
	if err != nil {
		return nil, nil, err
	}
	return u, sess, nil
}

func (s *AuthService) RegisterCustomerByStaff(
	ctx context.Context,
	fullName, phone, email, babyName string,
	babyDOB *time.Time,
	momCondition, medicalNotes string,
) (*domain.User, error) {
	fullName = strings.TrimSpace(fullName)
	phone = strings.TrimSpace(phone)
	email = strings.TrimSpace(strings.ToLower(email))
	if fullName == "" || phone == "" {
		return nil, fmt.Errorf("%w: full name and phone number required", ErrValidation)
	}
	if email == "" {
		cleanPhone := strings.ReplaceAll(strings.ReplaceAll(phone, "+", ""), " ", "")
		email = cleanPhone + "@wangga.local"
	}
	if existing, err := s.users.ByEmail(ctx, email); err == nil && existing != nil {
		return nil, fmt.Errorf("%w: user with email %s already exists", ErrValidation, email)
	}
	if existing, err := s.users.ByPhone(ctx, phone); err == nil && existing != nil {
		return nil, fmt.Errorf("%w: customer with phone %s already registered as %s", ErrValidation, phone, existing.FullName)
	}

	tempPass := "Wangga@" + uuid.New().String()[:8]
	hash, err := auth.HashPassword(tempPass)
	if err != nil {
		return nil, err
	}

	u := &domain.User{
		Email:        email,
		PasswordHash: hash,
		FullName:     fullName,
		Phone:        phone,
		Role:         domain.RoleCustomer,
		IsActive:     true,
		BabyName:     strings.TrimSpace(babyName),
		BabyDOB:      babyDOB,
		MomCondition: strings.TrimSpace(momCondition),
		MedicalNotes: strings.TrimSpace(medicalNotes),
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

func (s *AuthService) Login(ctx context.Context, email, password string) (*domain.User, *domain.Session, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	u, err := s.users.ByEmail(ctx, email)
	if errors.Is(err, repo.ErrNotFound) {
		return nil, nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, nil, err
	}
	if !u.IsActive {
		return nil, nil, ErrAccountDeactivated
	}
	if !auth.CheckPassword(u.PasswordHash, password) {
		return nil, nil, ErrInvalidCredentials
	}
	sess, err := s.createSession(ctx, u.ID)
	if err != nil {
		return nil, nil, err
	}
	return u, sess, nil
}

func (s *AuthService) Logout(ctx context.Context, sessionID string) error {
	return s.sessions.Delete(ctx, sessionID)
}

func (s *AuthService) UserFromSession(ctx context.Context, sessionID string) (*domain.User, error) {
	if sessionID == "" {
		return nil, ErrUnauthorized
	}
	sess, err := s.sessions.ByID(ctx, sessionID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil, ErrUnauthorized
	}
	if err != nil {
		return nil, err
	}
	if time.Now().After(sess.ExpiresAt) {
		_ = s.sessions.Delete(ctx, sessionID)
		return nil, ErrUnauthorized
	}
	u, err := s.users.ByID(ctx, sess.UserID)
	if errors.Is(err, repo.ErrNotFound) {
		return nil, ErrUnauthorized
	}
	if err != nil {
		return nil, err
	}
	if !u.IsActive {
		_ = s.sessions.Delete(ctx, sessionID)
		return nil, ErrAccountDeactivated
	}
	return u, nil
}

func (s *AuthService) createSession(ctx context.Context, userID uuid.UUID) (*domain.Session, error) {
	id, err := auth.NewSessionID()
	if err != nil {
		return nil, err
	}
	sess := &domain.Session{
		ID:        id,
		UserID:    userID,
		ExpiresAt: time.Now().Add(s.ttl),
	}
	if err := s.sessions.Create(ctx, sess); err != nil {
		return nil, err
	}
	return sess, nil
}

func (s *AuthService) EnsureAdmin(ctx context.Context, email, password, fullName string) error {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	return s.users.EnsureAdmin(ctx, strings.ToLower(email), hash, fullName)
}

func (s *AuthService) EnsureCS(ctx context.Context, email, password, fullName string) error {
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	return s.users.EnsureCS(ctx, strings.ToLower(email), hash, fullName)
}

func (s *AuthService) ListCustomers(ctx context.Context, query string) ([]domain.User, error) {
	return s.users.ListCustomers(ctx, query)
}

func (s *AuthService) SearchCustomers(ctx context.Context, query string) ([]domain.User, error) {
	return s.users.SearchCustomers(ctx, query, 20)
}

// UpdateCustomer validates and saves editable customer profile fields.
// Role, activity status, and password are never touched here.
func (s *AuthService) UpdateCustomer(
	ctx context.Context, id uuid.UUID,
	fullName, phone, email, babyName string,
	babyDOB *time.Time,
	momCondition, medicalNotes string,
) (*domain.User, error) {
	cust, err := s.users.ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if cust.Role != domain.RoleCustomer {
		return nil, fmt.Errorf("%w: not a customer account", ErrValidation)
	}
	fullName = strings.TrimSpace(fullName)
	phone = strings.TrimSpace(phone)
	email = strings.TrimSpace(strings.ToLower(email))
	if fullName == "" || phone == "" {
		return nil, fmt.Errorf("%w: full name and phone number required", ErrValidation)
	}
	if email == "" {
		email = cust.Email // keep existing identity; never regenerate silently
	}
	if existing, err := s.users.ByEmail(ctx, email); err == nil && existing != nil && existing.ID != id {
		return nil, fmt.Errorf("%w: user with email %s already exists", ErrValidation, email)
	}
	if existing, err := s.users.ByPhone(ctx, phone); err == nil && existing != nil && existing.ID != id {
		return nil, fmt.Errorf("%w: phone %s already registered as %s", ErrValidation, phone, existing.FullName)
	}
	cust.Email = email
	cust.FullName = fullName
	cust.Phone = phone
	cust.BabyName = strings.TrimSpace(babyName)
	cust.BabyDOB = babyDOB
	cust.MomCondition = strings.TrimSpace(momCondition)
	cust.MedicalNotes = strings.TrimSpace(medicalNotes)
	if err := s.users.Update(ctx, cust); err != nil {
		return nil, err
	}
	return cust, nil
}

// DeleteCustomer removes a customer only when no booking history references
// them; otherwise it refuses so staff deactivates the profile instead.
func (s *AuthService) DeleteCustomer(ctx context.Context, id uuid.UUID) error {
	cust, err := s.users.ByID(ctx, id)
	if err != nil {
		return err
	}
	if cust.Role != domain.RoleCustomer {
		return fmt.Errorf("%w: not a customer account", ErrValidation)
	}
	n, err := s.users.CustomerBookingCount(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: customer has booking history — deactivate instead of removing", ErrValidation)
	}
	return s.users.DeleteCustomer(ctx, id)
}

func (s *AuthService) ListCS(ctx context.Context) ([]domain.User, error) {
	return s.users.ListByRole(ctx, domain.RoleCS)
}

func (s *AuthService) GetUser(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	return s.users.ByID(ctx, id)
}

func (s *AuthService) CreateCS(ctx context.Context, email, password, fullName, phone string) (*domain.User, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	fullName = strings.TrimSpace(fullName)
	phone = strings.TrimSpace(phone)
	if email == "" || fullName == "" || len(password) < 8 {
		return nil, fmt.Errorf("%w: email, name, password (min 8) required", ErrValidation)
	}
	if _, err := s.users.ByEmail(ctx, email); err == nil {
		return nil, ErrEmailTaken
	} else if !errors.Is(err, repo.ErrNotFound) {
		return nil, err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return nil, err
	}
	u := &domain.User{
		Email:        email,
		PasswordHash: hash,
		FullName:     fullName,
		Phone:        phone,
		Role:         domain.RoleCS,
		IsActive:     true,
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// UpdateProfile lets a user edit their own display name and profile photo.
// Uploads are stored paths ("" clears); links are validated http(s) URLs.
func (s *AuthService) UpdateProfile(ctx context.Context, id uuid.UUID, fullName, photoPath, photoURL string) (*domain.User, error) {
	u, err := s.users.ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	fullName = strings.TrimSpace(fullName)
	if n := utf8.RuneCountInString(fullName); n < 2 || n > 80 {
		return nil, fmt.Errorf("%w: name must be 2-80 characters", ErrValidation)
	}
	if err := ValidateImageURL(photoURL); err != nil {
		return nil, err
	}
	u.FullName = fullName
	u.PhotoPath = strings.TrimSpace(photoPath)
	u.PhotoURL = strings.TrimSpace(photoURL)
	if err := s.users.Update(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// ResolveAvatar picks the display picture: local upload first, then
// internet link, then "" (templates fall back to initials avatars).
func ResolveAvatar(u domain.User) string {
	if u.PhotoPath != "" {
		return "/uploads/avatars/" + path.Base(u.PhotoPath)
	}
	return strings.TrimSpace(u.PhotoURL)
}

func (s *AuthService) SetUserActive(ctx context.Context, userID uuid.UUID, active bool) error {
	if err := s.users.SetActive(ctx, userID, active); err != nil {
		return err
	}
	if !active {
		_ = s.sessions.DeleteByUserID(ctx, userID)
	}
	return nil
}

func (s *AuthService) ResetPassword(ctx context.Context, userID uuid.UUID, newPassword string) error {
	newPassword = strings.TrimSpace(newPassword)
	if len(newPassword) < 8 {
		return fmt.Errorf("%w: password must be at least 8 characters", ErrValidation)
	}
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return err
	}
	if err := s.users.UpdatePassword(ctx, userID, hash); err != nil {
		return err
	}
	_ = s.sessions.DeleteByUserID(ctx, userID)
	return nil
}
