// Package mysqlrepo implements RBAC persistence with MySQL.
package mysqlrepo

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"service_rpc/internal/rbac"
)

// Repository is the MySQL implementation of rbac.Repository.
type Repository struct {
	db *sql.DB
}

// New creates an RBAC repository backed by the process database pool.
func New(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// ReplaceUserRoles validates every requested role before deleting the current
// set. The transaction prevents an invalid replacement from leaving a user
// with a partially updated or empty role set.
func (r *Repository) ReplaceUserRoles(ctx context.Context, userID uint64, roleCodes []string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin role replacement: %w", err)
	}
	defer tx.Rollback()

	var userExists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE id = ?)", userID).Scan(&userExists); err != nil {
		return fmt.Errorf("check role target user: %w", err)
	}
	if !userExists {
		return rbac.ErrUserNotFound
	}

	roleIDs, err := findRoleIDs(ctx, tx, roleCodes)
	if err != nil {
		return err
	}
	if len(roleIDs) != len(roleCodes) {
		return rbac.ErrRoleAssignmentConflict
	}

	if _, err := tx.ExecContext(ctx, "DELETE FROM user_roles WHERE user_id = ?", userID); err != nil {
		return fmt.Errorf("clear user roles: %w", err)
	}
	for _, code := range roleCodes {
		if _, err := tx.ExecContext(ctx, "INSERT INTO user_roles (user_id, role_id) VALUES (?, ?)", userID, roleIDs[code]); err != nil {
			return fmt.Errorf("assign user role: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit role replacement: %w", err)
	}
	return nil
}

func findRoleIDs(ctx context.Context, tx *sql.Tx, roleCodes []string) (map[string]uint64, error) {
	if len(roleCodes) == 0 {
		return map[string]uint64{}, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(roleCodes)), ",")
	args := make([]any, len(roleCodes))
	for i, code := range roleCodes {
		args[i] = code
	}
	rows, err := tx.QueryContext(ctx, "SELECT id, code FROM roles WHERE code IN ("+placeholders+")", args...)
	if err != nil {
		return nil, fmt.Errorf("find role IDs: %w", err)
	}
	defer rows.Close()

	result := make(map[string]uint64, len(roleCodes))
	for rows.Next() {
		var id uint64
		var code string
		if err := rows.Scan(&id, &code); err != nil {
			return nil, fmt.Errorf("scan role ID: %w", err)
		}
		result[code] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate role IDs: %w", err)
	}
	return result, nil
}

// UserRoles returns sorted role codes for stable API output.
func (r *Repository) UserRoles(ctx context.Context, userID uint64) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT r.code
		FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = ?
		ORDER BY r.code
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("query user roles: %w", err)
	}
	defer rows.Close()

	roles := make([]string, 0)
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			return nil, fmt.Errorf("scan user role: %w", err)
		}
		roles = append(roles, code)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate user roles: %w", err)
	}
	return roles, nil
}

// HasPermission resolves current role-permission relationships in MySQL.
func (r *Repository) HasPermission(ctx context.Context, userID uint64, permissionCode string) (bool, error) {
	var allowed bool
	err := r.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM user_roles ur
			JOIN role_permissions rp ON rp.role_id = ur.role_id
			JOIN permissions p ON p.id = rp.permission_id
			WHERE ur.user_id = ? AND p.code = ?
		)
	`, userID, permissionCode).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("check user permission: %w", err)
	}
	return allowed, nil
}
