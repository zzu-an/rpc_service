package mysqlrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"service_rpc/internal/config"
	"service_rpc/internal/platform/database"
	"service_rpc/internal/rbac"
)

func TestRepositoryRoleReplacementAndPermissions(t *testing.T) {
	dsn := os.Getenv("SERVICE_RPC_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("SERVICE_RPC_MYSQL_TEST_DSN is not set")
	}
	db, err := database.OpenMySQL(context.Background(), config.MySQLConfig{
		DataSource:             dsn,
		MaxOpenConns:           4,
		MaxIdleConns:           2,
		ConnMaxLifetimeSeconds: 60,
	})
	if err != nil {
		t.Fatalf("open MySQL: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close MySQL: %v", err)
		}
	})

	email := fmt.Sprintf("rbac-%d@example.com", time.Now().UnixNano())
	result, err := db.ExecContext(context.Background(), `
		INSERT INTO users (email, password_hash, status) VALUES (?, 'test-hash', 1)
	`, email)
	if err != nil {
		t.Fatalf("insert test user: %v", err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("read test user ID: %v", err)
	}
	t.Cleanup(func() {
		if _, err := db.ExecContext(context.Background(), "DELETE FROM users WHERE id = ?", userID); err != nil {
			t.Errorf("clean test user: %v", err)
		}
	})

	service := rbac.NewService(New(db))
	if err := service.ReplaceUserRoles(context.Background(), uint64(userID), []string{"customer"}); err != nil {
		t.Fatalf("assign customer: %v", err)
	}
	roles, err := service.UserRoles(context.Background(), uint64(userID))
	if err != nil || !reflect.DeepEqual(roles, []string{"customer"}) {
		t.Fatalf("customer roles=%v error=%v", roles, err)
	}
	allowed, err := service.HasPermission(context.Background(), uint64(userID), "rbac:manage")
	if err != nil || allowed {
		t.Fatalf("customer rbac:manage allowed=%t error=%v", allowed, err)
	}

	if err := service.ReplaceUserRoles(context.Background(), uint64(userID), []string{"admin", "missing-role"}); !errors.Is(err, rbac.ErrRoleAssignmentConflict) {
		t.Fatalf("invalid replacement error=%v, want conflict", err)
	}
	roles, err = service.UserRoles(context.Background(), uint64(userID))
	if err != nil || !reflect.DeepEqual(roles, []string{"customer"}) {
		t.Fatalf("roles changed after invalid replacement: roles=%v error=%v", roles, err)
	}

	if err := service.ReplaceUserRoles(context.Background(), uint64(userID), []string{"admin", "customer"}); err != nil {
		t.Fatalf("assign admin and customer: %v", err)
	}
	roles, err = service.UserRoles(context.Background(), uint64(userID))
	if err != nil || !reflect.DeepEqual(roles, []string{"admin", "customer"}) {
		t.Fatalf("admin roles=%v error=%v", roles, err)
	}
	allowed, err = service.HasPermission(context.Background(), uint64(userID), "rbac:manage")
	if err != nil || !allowed {
		t.Fatalf("admin rbac:manage allowed=%t error=%v", allowed, err)
	}
}
