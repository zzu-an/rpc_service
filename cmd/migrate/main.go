// Command migrate applies the repository's versioned MySQL migrations.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"

	"github.com/golang-migrate/migrate/v4"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/zeromicro/go-zero/core/conf"

	"service_rpc/internal/config"
	"service_rpc/internal/platform/database"
)

var configFile = flag.String("f", "etc/store-api.yaml", "the config file")

func main() {
	flag.Parse()
	if err := run(flag.Args()); err != nil {
		log.Fatal(err)
	}
}

func run(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: migrate [-f config] <up|down|version>")
	}

	var c config.Config
	if err := conf.Load(*configFile, &c); err != nil {
		return fmt.Errorf("load migration config: %w", err)
	}

	db, err := database.OpenMySQL(context.Background(), c.MySQL)
	if err != nil {
		return fmt.Errorf("initialize migration database: %w", err)
	}
	driver, err := migratemysql.WithInstance(db, &migratemysql.Config{})
	if err != nil {
		db.Close()
		return fmt.Errorf("initialize MySQL migration driver: %w", err)
	}

	m, err := migrate.NewWithDatabaseInstance("file://migrations", "mysql", driver)
	if err != nil {
		driver.Close()
		return fmt.Errorf("initialize migrations: %w", err)
	}
	defer func() {
		if sourceErr, databaseErr := m.Close(); sourceErr != nil || databaseErr != nil {
			log.Printf("close migration resources: source=%v database=%v", sourceErr, databaseErr)
		}
	}()

	switch args[0] {
	case "up":
		return allowNoChange(m.Up())
	case "down":
		// One-step rollback is deliberate: an unqualified command must never
		// silently erase every schema version in a developer database.
		return allowNoChange(m.Steps(-1))
	case "version":
		version, dirty, err := m.Version()
		if err != nil {
			return allowNoChange(err)
		}
		fmt.Printf("version=%d dirty=%t\n", version, dirty)
		return nil
	default:
		return fmt.Errorf("unknown migration command %q", args[0])
	}
}

func allowNoChange(err error) error {
	if errors.Is(err, migrate.ErrNoChange) {
		fmt.Println("no change")
		return nil
	}
	return err
}
