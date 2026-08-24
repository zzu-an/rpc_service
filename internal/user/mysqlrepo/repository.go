// Package mysqlrepo implements user persistence with MySQL.
package mysqlrepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	gomysql "github.com/go-sql-driver/mysql"

	"service_rpc/internal/user"
)

// Repository is the MySQL implementation of user.Repository.
type Repository struct {
	db *sql.DB
}

// New creates a user repository backed by an existing process-level pool.
func New(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// Create relies on uk_users_email as the final concurrent duplicate guard.
func (r *Repository) Create(ctx context.Context, email, passwordHash string) (user.User, error) {
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO users (email, password_hash, status)
		VALUES (?, ?, 1)
	`, email, passwordHash)
	if err != nil {
		var mysqlErr *gomysql.MySQLError
		if errors.As(err, &mysqlErr) && mysqlErr.Number == 1062 {
			return user.User{}, user.ErrUserAlreadyExists
		}
		return user.User{}, fmt.Errorf("insert user: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return user.User{}, fmt.Errorf("read inserted user id: %w", err)
	}
	return user.User{
		ID:        uint64(id),
		Email:     email,
		CreatedAt: time.Now().UTC(),
	}, nil
}

// FindCredentialByEmail returns password material only to the authentication
// service. Unknown emails map to the same domain error as invalid passwords.
func (r *Repository) FindCredentialByEmail(ctx context.Context, email string) (user.Credential, error) {
	var credential user.Credential
	var status uint8
	err := r.db.QueryRowContext(ctx, `
		SELECT id, email, password_hash, status, created_at
		FROM users
		WHERE email = ?
	`, email).Scan(
		&credential.User.ID,
		&credential.User.Email,
		&credential.PasswordHash,
		&status,
		&credential.User.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return user.Credential{}, user.ErrInvalidCredentials
	}
	if err != nil {
		return user.Credential{}, fmt.Errorf("find user credential: %w", err)
	}
	credential.Active = status == 1
	return credential, nil
}

// FindByID returns only active users so a disabled account cannot continue to
// use an already issued access token for current-user lookups.
func (r *Repository) FindByID(ctx context.Context, id uint64) (user.User, error) {
	var found user.User
	err := r.db.QueryRowContext(ctx, `
		SELECT id, email, created_at
		FROM users
		WHERE id = ? AND status = 1
	`, id).Scan(&found.ID, &found.Email, &found.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return user.User{}, user.ErrUserNotFound
	}
	if err != nil {
		return user.User{}, fmt.Errorf("find active user by id: %w", err)
	}
	return found, nil
}
