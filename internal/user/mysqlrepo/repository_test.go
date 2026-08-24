package mysqlrepo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"service_rpc/internal/config"
	"service_rpc/internal/platform/database"
	"service_rpc/internal/user"
)

func TestRepositoryConcurrentDuplicateEmail(t *testing.T) {
	dsn := os.Getenv("SERVICE_RPC_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("SERVICE_RPC_MYSQL_TEST_DSN is not set")
	}

	db, err := database.OpenMySQL(context.Background(), config.MySQLConfig{
		DataSource:             dsn,
		MaxOpenConns:           10,
		MaxIdleConns:           5,
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

	email := fmt.Sprintf("concurrent-%d@example.com", time.Now().UnixNano())
	t.Cleanup(func() {
		if _, err := db.ExecContext(context.Background(), "DELETE FROM users WHERE email = ?", email); err != nil {
			t.Errorf("clean test user: %v", err)
		}
	})

	repository := New(db)
	const attempts = 10
	var successes atomic.Int32
	var duplicates atomic.Int32
	var unexpected atomic.Value
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := repository.Create(context.Background(), email, "test-hash")
			switch {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, user.ErrUserAlreadyExists):
				duplicates.Add(1)
			default:
				unexpected.Store(err)
			}
		}()
	}
	wg.Wait()

	if value := unexpected.Load(); value != nil {
		t.Fatalf("unexpected create error: %v", value)
	}
	if successes.Load() != 1 || duplicates.Load() != attempts-1 {
		t.Fatalf("successes=%d duplicates=%d, want 1 and %d", successes.Load(), duplicates.Load(), attempts-1)
	}
}

func TestRepositoryCredentialQueries(t *testing.T) {
	dsn := os.Getenv("SERVICE_RPC_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("SERVICE_RPC_MYSQL_TEST_DSN is not set")
	}

	db, err := database.OpenMySQL(context.Background(), config.MySQLConfig{
		DataSource:             dsn,
		MaxOpenConns:           3,
		MaxIdleConns:           1,
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

	email := fmt.Sprintf("credential-%d@example.com", time.Now().UnixNano())
	t.Cleanup(func() {
		if _, err := db.ExecContext(context.Background(), "DELETE FROM users WHERE email = ?", email); err != nil {
			t.Errorf("clean test user: %v", err)
		}
	})

	repository := New(db)
	registration := user.NewService(repository)
	authentication := user.NewAuthService(repository)
	created, err := registration.Register(context.Background(), email, "correct-password")
	if err != nil {
		t.Fatalf("register test user: %v", err)
	}

	authenticated, err := authentication.Authenticate(context.Background(), "  "+strings.ToUpper(email)+"  ", "correct-password")
	if err != nil {
		t.Fatalf("authenticate test user: %v", err)
	}
	if authenticated.ID != created.ID {
		t.Fatalf("authenticated ID=%d, want %d", authenticated.ID, created.ID)
	}

	found, err := authentication.CurrentUser(context.Background(), created.ID)
	if err != nil || found.Email != email {
		t.Fatalf("CurrentUser() = %+v, %v", found, err)
	}
	if _, err := db.ExecContext(context.Background(), "UPDATE users SET status = 2 WHERE id = ?", created.ID); err != nil {
		t.Fatalf("disable test user: %v", err)
	}
	if _, err := authentication.CurrentUser(context.Background(), created.ID); !errors.Is(err, user.ErrUserNotFound) {
		t.Fatalf("disabled CurrentUser() error = %v, want ErrUserNotFound", err)
	}
}
