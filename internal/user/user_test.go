package user

import (
	"context"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

type recordingRepository struct {
	createdEmail string
	createdHash  string
	createErr    error
}

func (r *recordingRepository) Create(_ context.Context, email, passwordHash string) (User, error) {
	r.createdEmail = email
	r.createdHash = passwordHash
	return User{ID: 42, Email: email}, r.createErr
}

func TestServiceRegisterNormalizesAndHashesPassword(t *testing.T) {
	repository := &recordingRepository{}
	service := NewService(repository)
	password := "correct horse battery staple"

	created, err := service.Register(context.Background(), "  USER@Example.COM ", password)
	if err != nil {
		t.Fatalf("Register() error: %v", err)
	}
	if created.ID != 42 || created.Email != "user@example.com" {
		t.Fatalf("created user = %+v", created)
	}
	if repository.createdEmail != "user@example.com" {
		t.Fatalf("stored email = %q, want user@example.com", repository.createdEmail)
	}
	if repository.createdHash == password || strings.Contains(repository.createdHash, password) {
		t.Fatal("repository received plaintext password")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(repository.createdHash), []byte(password)); err != nil {
		t.Fatalf("stored hash does not match password: %v", err)
	}
}

func TestServiceRegisterRejectsInvalidCredentials(t *testing.T) {
	tests := []struct {
		name     string
		email    string
		password string
		wantErr  error
	}{
		{name: "invalid email", email: "not-an-email", password: "password123", wantErr: ErrInvalidEmail},
		{name: "short password", email: "user@example.com", password: "short", wantErr: ErrInvalidPassword},
		{name: "bcrypt overflow", email: "user@example.com", password: strings.Repeat("a", 73), wantErr: ErrInvalidPassword},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repository := &recordingRepository{}
			_, err := NewService(repository).Register(context.Background(), tt.email, tt.password)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Register() error = %v, want %v", err, tt.wantErr)
			}
			if repository.createdEmail != "" {
				t.Fatal("repository called for invalid credentials")
			}
		})
	}
}
