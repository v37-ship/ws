package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"spa-booking/internal/auth"
	"spa-booking/internal/domain"
	"spa-booking/internal/repo"

	"github.com/google/uuid"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrEmailTaken         = errors.New("email already registered")
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
		Phone:        strings.TrimSpace(phone),
		Role:         domain.RoleCustomer,
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

func (s *AuthService) Login(ctx context.Context, email, password string) (*domain.User, *domain.Session, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	u, err := s.users.ByEmail(ctx, email)
	if errors.Is(err, repo.ErrNotFound) {
		return nil, nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, nil, err
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
	return u, err
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

func (s *AuthService) GetUser(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	return s.users.ByID(ctx, id)
}

func (s *AuthService) CreateCustomer(ctx context.Context, email, password, fullName, phone string) (*domain.User, error) {
	email = strings.TrimSpace(strings.ToLower(email))
	fullName = strings.TrimSpace(fullName)
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
		Email: email, PasswordHash: hash, FullName: fullName,
		Phone: strings.TrimSpace(phone), Role: domain.RoleCustomer,
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

func (s *AuthService) UpdateCustomer(ctx context.Context, id uuid.UUID, email, fullName, phone, newPassword string) (*domain.User, error) {
	u, err := s.users.ByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if u.Role != domain.RoleCustomer {
		return nil, fmt.Errorf("%w: not a customer account", ErrValidation)
	}
	email = strings.TrimSpace(strings.ToLower(email))
	fullName = strings.TrimSpace(fullName)
	if email == "" || fullName == "" {
		return nil, fmt.Errorf("%w: email and name required", ErrValidation)
	}
	if other, err := s.users.ByEmail(ctx, email); err == nil && other.ID != id {
		return nil, ErrEmailTaken
	} else if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return nil, err
	}
	u.Email = email
	u.FullName = fullName
	u.Phone = strings.TrimSpace(phone)
	if err := s.users.Update(ctx, u); err != nil {
		return nil, err
	}
	if newPassword != "" {
		if len(newPassword) < 8 {
			return nil, fmt.Errorf("%w: password min 8", ErrValidation)
		}
		hash, err := auth.HashPassword(newPassword)
		if err != nil {
			return nil, err
		}
		if err := s.users.UpdatePassword(ctx, id, hash); err != nil {
			return nil, err
		}
	}
	return u, nil
}

func (s *AuthService) CreateStaffUser(ctx context.Context, email, password, fullName, phone string, role domain.Role) (*domain.User, error) {
	if role != domain.RoleCS && role != domain.RoleAdmin {
		return nil, fmt.Errorf("%w: invalid staff role", ErrValidation)
	}
	email = strings.TrimSpace(strings.ToLower(email))
	fullName = strings.TrimSpace(fullName)
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
		Email: email, PasswordHash: hash, FullName: fullName,
		Phone: strings.TrimSpace(phone), Role: role,
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}
