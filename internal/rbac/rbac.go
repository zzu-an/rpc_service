// Package rbac contains role-based authorization use cases.
package rbac

import (
	"context"
	"errors"
	"sort"
	"strings"
)

var (
	// ErrRoleAssignmentConflict indicates that one or more requested role codes
	// do not exist or cannot be assigned as a complete set.
	ErrRoleAssignmentConflict = errors.New("role assignment conflict")
	// ErrUserNotFound indicates that the role target user does not exist.
	ErrUserNotFound = errors.New("RBAC user not found")
)

// Repository is the persistence boundary required by RBAC use cases.
type Repository interface {
	ReplaceUserRoles(ctx context.Context, userID uint64, roleCodes []string) error
	UserRoles(ctx context.Context, userID uint64) ([]string, error)
	HasPermission(ctx context.Context, userID uint64, permissionCode string) (bool, error)
}

// Service implements role assignment and authorization queries.
type Service struct {
	repository Repository
}

// NewService creates an RBAC service.
func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

// ReplaceUserRoles normalizes the requested role set before the repository
// replaces it atomically. Sorting makes writes and tests deterministic.
func (s *Service) ReplaceUserRoles(ctx context.Context, userID uint64, roleCodes []string) error {
	if userID == 0 {
		return ErrUserNotFound
	}
	unique := make(map[string]struct{}, len(roleCodes))
	for _, code := range roleCodes {
		code = strings.TrimSpace(code)
		if code == "" {
			return ErrRoleAssignmentConflict
		}
		unique[code] = struct{}{}
	}
	normalized := make([]string, 0, len(unique))
	for code := range unique {
		normalized = append(normalized, code)
	}
	sort.Strings(normalized)
	return s.repository.ReplaceUserRoles(ctx, userID, normalized)
}

// UserRoles returns stable role codes for presentation and policy decisions.
func (s *Service) UserRoles(ctx context.Context, userID uint64) ([]string, error) {
	return s.repository.UserRoles(ctx, userID)
}

// HasPermission checks current MySQL-backed RBAC state. No token or cache
// snapshot is consulted, so role changes affect the next request.
func (s *Service) HasPermission(ctx context.Context, userID uint64, permissionCode string) (bool, error) {
	if userID == 0 || strings.TrimSpace(permissionCode) == "" {
		return false, nil
	}
	return s.repository.HasPermission(ctx, userID, permissionCode)
}
