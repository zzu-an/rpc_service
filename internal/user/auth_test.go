package user

import (
	"context"
	"errors"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

type credentialRepository struct {
	credential Credential
	findErr    error
	user       User
	userErr    error
	lastEmail  string
}

func (r *credentialRepository) FindCredentialByEmail(_ context.Context, email string) (Credential, error) {
	r.lastEmail = email
	return r.credential, r.findErr
}

func (r *credentialRepository) FindByID(_ context.Context, _ uint64) (User, error) {
	return r.user, r.userErr
}

func TestAuthServiceAuthenticate(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("generate test hash: %v", err)
	}
	repository := &credentialRepository{credential: Credential{
		User:         User{ID: 9, Email: "user@example.com"},
		PasswordHash: string(hash),
		Active:       true,
	}}
	service := NewAuthService(repository)

	got, err := service.Authenticate(context.Background(), " USER@Example.com ", "correct-password")
	if err != nil {
		t.Fatalf("Authenticate() error: %v", err)
	}
	if got.ID != 9 || repository.lastEmail != "user@example.com" {
		t.Fatalf("user=%+v lookup email=%q", got, repository.lastEmail)
	}
}

func TestAuthServiceHidesCredentialFailureReason(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("generate test hash: %v", err)
	}
	tests := []struct {
		name       string
		repository *credentialRepository
		email      string
		password   string
	}{
		{
			name:       "unknown email",
			repository: &credentialRepository{findErr: ErrInvalidCredentials},
			email:      "unknown@example.com",
			password:   "any-password",
		},
		{
			name: "wrong password",
			repository: &credentialRepository{credential: Credential{
				PasswordHash: string(hash), Active: true,
			}},
			email:    "user@example.com",
			password: "wrong-password",
		},
		{
			name: "disabled user",
			repository: &credentialRepository{credential: Credential{
				PasswordHash: string(hash), Active: false,
			}},
			email:    "user@example.com",
			password: "correct-password",
		},
		{
			name:       "invalid email syntax",
			repository: &credentialRepository{},
			email:      "not-an-email",
			password:   "any-password",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewAuthService(tt.repository).Authenticate(context.Background(), tt.email, tt.password)
			if !errors.Is(err, ErrInvalidCredentials) {
				t.Fatalf("Authenticate() error = %v, want ErrInvalidCredentials", err)
			}
		})
	}
}

func TestAuthServiceCurrentUser(t *testing.T) {
	repository := &credentialRepository{user: User{ID: 3, Email: "active@example.com"}}
	got, err := NewAuthService(repository).CurrentUser(context.Background(), 3)
	if err != nil {
		t.Fatalf("CurrentUser() error: %v", err)
	}
	if got.ID != 3 {
		t.Fatalf("CurrentUser() = %+v", got)
	}
}
