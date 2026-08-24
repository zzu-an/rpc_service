// Package database owns infrastructure-level database connections.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"service_rpc/internal/config"
)

const mysqlPingTimeout = 5 * time.Second

// OpenMySQL opens, configures, and verifies a MySQL connection pool. Ping is
// intentionally bounded so a bad address cannot leave application startup
// hanging indefinitely. The returned errors never include the configured DSN.
func OpenMySQL(ctx context.Context, c config.MySQLConfig) (*sql.DB, error) {
	if err := validateMySQLConfig(c); err != nil {
		return nil, err
	}

	db, err := sql.Open("mysql", c.DataSource)
	if err != nil {
		return nil, fmt.Errorf("open MySQL driver: %w", err)
	}
	db.SetMaxOpenConns(c.MaxOpenConns)
	db.SetMaxIdleConns(c.MaxIdleConns)
	db.SetConnMaxLifetime(time.Duration(c.ConnMaxLifetimeSeconds) * time.Second)

	pingCtx, cancel := context.WithTimeout(ctx, mysqlPingTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping MySQL: %w", err)
	}

	return db, nil
}

func validateMySQLConfig(c config.MySQLConfig) error {
	if c.DataSource == "" {
		return errors.New("MySQL data source is required")
	}
	if c.MaxOpenConns <= 0 {
		return errors.New("MySQL max open connections must be positive")
	}
	if c.MaxIdleConns < 0 || c.MaxIdleConns > c.MaxOpenConns {
		return errors.New("MySQL max idle connections must be between zero and max open connections")
	}
	if c.ConnMaxLifetimeSeconds <= 0 {
		return errors.New("MySQL connection lifetime must be positive")
	}
	return nil
}
