// Package auth contains identity-token primitives. Business authorization is
// intentionally outside this package and remains a separate RBAC concern.
package auth

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const tokenIssuer = "service_rpc"

var (
	// ErrTokenInvalid covers malformed, incorrectly signed, or semantically
	// invalid access tokens.
	ErrTokenInvalid = errors.New("invalid access token")
	// ErrTokenExpired is separate so the HTTP boundary can return TOKEN_EXPIRED.
	ErrTokenExpired = errors.New("access token expired")
)

// TokenManager signs and verifies short-lived HS256 access tokens.
type TokenManager struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

// NewTokenManager validates token configuration and uses the system clock.
func NewTokenManager(secret string, ttl time.Duration) (*TokenManager, error) {
	return newTokenManager(secret, ttl, time.Now)
}

func newTokenManager(secret string, ttl time.Duration, now func() time.Time) (*TokenManager, error) {
	if len(secret) < 32 {
		return nil, errors.New("access token secret must be at least 32 bytes")
	}
	if ttl <= 0 {
		return nil, errors.New("access token TTL must be positive")
	}
	return &TokenManager{secret: []byte(secret), ttl: ttl, now: now}, nil
}

// Issue creates a token whose only application identity is the user ID in
// sub. Roles and permissions are deliberately excluded so RBAC changes do not
// remain frozen inside old tokens.
func (m *TokenManager) Issue(userID uint64) (string, error) {
	if userID == 0 {
		return "", errors.New("access token user ID must be positive")
	}
	now := m.now().UTC()
	claims := jwt.RegisteredClaims{
		Issuer:    tokenIssuer,
		Subject:   strconv.FormatUint(userID, 10),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(m.ttl)),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
}

// ParseUserID verifies signature, algorithm, issuer and time claims before
// returning the authenticated user ID.
func (m *TokenManager) ParseUserID(tokenText string) (uint64, error) {
	var claims jwt.RegisteredClaims
	token, err := jwt.ParseWithClaims(
		tokenText,
		&claims,
		func(token *jwt.Token) (any, error) {
			if token.Method != jwt.SigningMethodHS256 {
				return nil, fmt.Errorf("unexpected signing method %q", token.Method.Alg())
			}
			return m.secret, nil
		},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithTimeFunc(m.now),
	)
	if errors.Is(err, jwt.ErrTokenExpired) {
		return 0, ErrTokenExpired
	}
	if err != nil || token == nil || !token.Valid {
		return 0, ErrTokenInvalid
	}

	userID, err := strconv.ParseUint(claims.Subject, 10, 64)
	if err != nil || userID == 0 {
		return 0, ErrTokenInvalid
	}
	return userID, nil
}

// ExpiresInSeconds reports the configured Access Token lifetime.
func (m *TokenManager) ExpiresInSeconds() int64 {
	return int64(m.ttl / time.Second)
}
