// Package dbmigrate applies the service's embedded PostgreSQL migrations.
package dbmigrate

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/lib/pq"

	"todo-service/migrations"
)

// Apply applies all up migrations or rolls the database back to version zero.
func Apply(dsn, direction string) error {
	if direction != "up" && direction != "down" {
		return fmt.Errorf("direction must be up or down, got %q", direction)
	}
	if dsn == "" {
		return fmt.Errorf("TODO_DATABASE_URL is required")
	}
	source, err := iofs.New(migrations.Files, ".")
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("open postgres: %w", err)
	}
	driver, err := postgres.WithInstance(db, &postgres.Config{
		MigrationsTable:       "golang_migrations",
		MultiStatementEnabled: false,
	})
	if err != nil {
		db.Close()
		source.Close()
		return fmt.Errorf("configure postgres migration driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		driver.Close()
		source.Close()
		return fmt.Errorf("create migration runner: %w", err)
	}
	defer m.Close()

	switch direction {
	case "up":
		err = m.Up()
	case "down":
		err = m.Down()
	}
	if errors.Is(err, migrate.ErrNoChange) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("migrate %s: %w", direction, err)
	}
	return nil
}
