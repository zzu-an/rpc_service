package database

import (
	"context"
	"os"
	"testing"

	"github.com/go-sql-driver/mysql"

	"service_rpc/internal/config"
)

func TestOpenMySQLRejectsInvalidConfig(t *testing.T) {
	_, err := OpenMySQL(context.Background(), config.MySQLConfig{})
	if err == nil {
		t.Fatal("OpenMySQL() error = nil, want validation error")
	}
}

func TestOpenMySQLIntegration(t *testing.T) {
	dsn := os.Getenv("SERVICE_RPC_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("SERVICE_RPC_MYSQL_TEST_DSN is not set")
	}
	parsedDSN, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse test DSN: %v", err)
	}

	db, err := OpenMySQL(context.Background(), config.MySQLConfig{
		DataSource:             dsn,
		MaxOpenConns:           2,
		MaxIdleConns:           1,
		ConnMaxLifetimeSeconds: 60,
	})
	if err != nil {
		t.Fatalf("OpenMySQL() error: %v", err)
	}
	defer db.Close()

	var databaseName string
	if err := db.QueryRowContext(context.Background(), "SELECT DATABASE()").Scan(&databaseName); err != nil {
		t.Fatalf("query current database: %v", err)
	}
	// Deriving the expected name from the supplied DSN keeps this integration
	// test usable with an isolated database instead of coupling it to dev data.
	if databaseName != parsedDSN.DBName {
		t.Fatalf("database = %q, want %q", databaseName, parsedDSN.DBName)
	}
}
