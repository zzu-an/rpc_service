package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func TestTokenManagerIssueAndParse(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	manager, err := newTokenManager(testSecret, 15*time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatalf("newTokenManager() error: %v", err)
	}
	tokenText, err := manager.Issue(42)
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}
	userID, err := manager.ParseUserID(tokenText)
	if err != nil || userID != 42 {
		t.Fatalf("ParseUserID() = %d, %v", userID, err)
	}

	var claims jwt.MapClaims
	if _, _, err := jwt.NewParser().ParseUnverified(tokenText, claims); err != nil {
		t.Fatalf("ParseUnverified() error: %v", err)
	}
	for claim := range claims {
		switch claim {
		case "iss", "sub", "iat", "exp":
		default:
			t.Fatalf("unexpected application claim %q; roles and permissions must not be embedded", claim)
		}
	}
}

func TestTokenManagerRejectsExpiredAndTamperedTokens(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	manager, err := newTokenManager(testSecret, time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatalf("newTokenManager() error: %v", err)
	}
	tokenText, err := manager.Issue(7)
	if err != nil {
		t.Fatalf("Issue() error: %v", err)
	}

	now = now.Add(2 * time.Minute)
	if _, err := manager.ParseUserID(tokenText); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired ParseUserID() error = %v, want ErrTokenExpired", err)
	}
	now = now.Add(-2 * time.Minute)
	if _, err := manager.ParseUserID(tokenText + "tampered"); !errors.Is(err, ErrTokenInvalid) {
		t.Fatalf("tampered ParseUserID() error = %v, want ErrTokenInvalid", err)
	}
}

func TestTokenManagerValidatesConfiguration(t *testing.T) {
	if _, err := NewTokenManager("short", time.Minute); err == nil {
		t.Fatal("short secret error = nil")
	}
	if _, err := NewTokenManager(testSecret, 0); err == nil {
		t.Fatal("zero TTL error = nil")
	}
}
