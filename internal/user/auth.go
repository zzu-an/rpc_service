package user

import (
	"context"
	"errors"

	"golang.org/x/crypto/bcrypt"
)

var (
	// ErrInvalidCredentials intentionally covers unknown, disabled, and
	// password-mismatch cases so callers cannot enumerate registered emails.
	ErrInvalidCredentials = errors.New("invalid credentials")
	// ErrUserNotFound indicates that an active user ID cannot be found.
	ErrUserNotFound = errors.New("user not found")
)

// Credential is restricted to the authentication boundary. Transport code
// receives only User and therefore cannot accidentally serialize the hash.
type Credential struct {
	User         User
	PasswordHash string
	Active       bool
}

// CredentialRepository supplies only the reads required by authentication.
type CredentialRepository interface {
	FindCredentialByEmail(ctx context.Context, email string) (Credential, error)
	FindByID(ctx context.Context, id uint64) (User, error)
}

// AuthService implements credential verification and current-user lookup.
type AuthService struct {
	repository CredentialRepository
	dummyHash  []byte
}

// NewAuthService creates an authentication service. A valid dummy bcrypt hash
// lets the unknown-user path still perform expensive password work, reducing
// an obvious timing signal for account enumeration.
func NewAuthService(repository CredentialRepository) *AuthService {
	dummyHash, err := bcrypt.GenerateFromPassword([]byte("dummy-password-never-used"), bcrypt.DefaultCost)
	if err != nil {
		panic("generate dummy bcrypt hash: " + err.Error())
	}
	return &AuthService{repository: repository, dummyHash: dummyHash}
}

// Authenticate verifies credentials without exposing which part failed.
func (s *AuthService) Authenticate(ctx context.Context, email, password string) (User, error) {
	normalizedEmail, valid := normalizeEmail(email)
	if !valid {
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
		return User{}, ErrInvalidCredentials
	}

	credential, err := s.repository.FindCredentialByEmail(ctx, normalizedEmail)
	if err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
			return User{}, ErrInvalidCredentials
		}
		return User{}, err
	}
	if !credential.Active {
		_ = bcrypt.CompareHashAndPassword([]byte(credential.PasswordHash), []byte(password))
		return User{}, ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(credential.PasswordHash), []byte(password)); err != nil {
		return User{}, ErrInvalidCredentials
	}
	return credential.User, nil
}

// CurrentUser returns only an active user for an already authenticated ID.
func (s *AuthService) CurrentUser(ctx context.Context, id uint64) (User, error) {
	return s.repository.FindByID(ctx, id)
}
