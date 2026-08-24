// Package user contains user-domain rules and application use cases.
package user

import (
	"context"
	"errors"
	"net/mail"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	// ErrInvalidEmail indicates that an email cannot be used as a login identity.
	ErrInvalidEmail = errors.New("invalid email")
	// ErrInvalidPassword indicates that a password violates the registration policy.
	ErrInvalidPassword = errors.New("invalid password")
	// ErrUserAlreadyExists is returned when the normalized email is already stored.
	ErrUserAlreadyExists = errors.New("user already exists")
)

// User is the domain representation returned by user use cases. Password
// hashes are intentionally absent so transport code cannot expose them by
// serializing this value.
type User struct {
	ID        uint64
	Email     string
	CreatedAt time.Time
}

// Repository is the persistence boundary required by current user use cases.
type Repository interface {
	Create(ctx context.Context, email, passwordHash string) (User, error)
}

// Service implements user application use cases.
type Service struct {
	repository Repository
}

// NewService creates a user service with its required persistence boundary.
func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

// Register validates and normalizes credentials before persisting a password
// hash. It deliberately does not perform a pre-insert existence query: only
// the database unique constraint can close the race between concurrent calls.
func (s *Service) Register(ctx context.Context, email, password string) (User, error) {
	normalizedEmail, ok := normalizeEmail(email)
	if !ok {
		return User{}, ErrInvalidEmail
	}
	if len(password) < 8 || len(password) > 72 {
		return User{}, ErrInvalidPassword
	}

	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	return s.repository.Create(ctx, normalizedEmail, string(passwordHash))
}

func normalizeEmail(email string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(email))
	parsed, err := mail.ParseAddress(normalized)
	return normalized, err == nil && parsed.Address == normalized
}
