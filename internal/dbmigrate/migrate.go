// Package dbmigrate applies the service's embedded PostgreSQL migrations.
package dbmigrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/lib/pq"

	"todo-service/migrations"
)

const (
	migrationLockID                int64 = 7_311_2026
	legacyMigrationInProgress      uint  = 2_147_483_647
	legacyMigrationInProgressLabel       = "golang-migrate-in-progress"
)

// Apply applies all up migrations or rolls the database back to version zero.
func Apply(dsn, direction string) (applyErr error) {
	if direction != "up" && direction != "down" {
		return fmt.Errorf("direction must be up or down, got %q", direction)
	}
	if dsn == "" {
		return fmt.Errorf("TODO_DATABASE_URL is required")
	}
	m, db, latestVersion, err := createRunner(dsn)
	if err != nil {
		return err
	}
	defer m.Close()
	lockConn, err := lockMigrations(db)
	if err != nil {
		return err
	}
	defer func() {
		applyErr = errors.Join(applyErr, unlockMigrations(lockConn))
	}()
	if _, err := prepareLegacyLedger(m, db, latestVersion, direction); err != nil {
		return err
	}
	if err := runMigrations(m, direction); err != nil {
		return err
	}
	currentVersion, _, err := migrationVersion(m)
	if err != nil {
		return err
	}
	if err := syncLegacyMigrationLedger(db, currentVersion, "golang-migrate"); err != nil {
		return fmt.Errorf("sync legacy migration ledger after %s: %w", direction, err)
	}
	return nil
}

func createRunner(dsn string) (*migrate.Migrate, *sql.DB, uint, error) {
	source, err := iofs.New(migrations.Files, ".")
	if err != nil {
		return nil, nil, 0, fmt.Errorf("load migrations: %w", err)
	}
	latestVersion, err := validateMigrationSequence(source)
	if err != nil {
		source.Close()
		return nil, nil, 0, err
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		source.Close()
		return nil, nil, 0, fmt.Errorf("open postgres: %w", err)
	}
	driver, err := postgres.WithInstance(db, &postgres.Config{
		MigrationsTable:       "golang_migrations",
		MultiStatementEnabled: false,
	})
	if err != nil {
		db.Close()
		source.Close()
		return nil, nil, 0, fmt.Errorf("configure postgres migration driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		driver.Close()
		source.Close()
		return nil, nil, 0, fmt.Errorf("create migration runner: %w", err)
	}
	return m, db, latestVersion, nil
}

func prepareLegacyLedger(m *migrate.Migrate, db *sql.DB, latestVersion uint, direction string) (uint, error) {
	currentVersion, dirty, err := migrationVersion(m)
	if err != nil {
		return 0, err
	}
	if dirty {
		return 0, fmt.Errorf("migration version %d is dirty", currentVersion)
	}
	if currentVersion > latestVersion {
		return 0, fmt.Errorf("database schema is at version %d, newer than this binary's latest migration %d", currentVersion, latestVersion)
	}
	legacyVersion, legacyName, err := legacyMigrationVersion(db)
	if err != nil {
		return 0, err
	}
	if legacyVersion > latestVersion && !(legacyVersion == legacyMigrationInProgress && legacyName == legacyMigrationInProgressLabel) {
		return 0, fmt.Errorf("legacy database schema is at version %d, newer than this binary's latest migration %d", legacyVersion, latestVersion)
	}
	needsMigration := (direction == "up" && currentVersion < latestVersion) || (direction == "down" && currentVersion > 0)
	if needsMigration {
		if err := syncLegacyMigrationLedger(db, legacyMigrationInProgress, legacyMigrationInProgressLabel); err != nil {
			return 0, fmt.Errorf("mark legacy migration ledger before %s: %w", direction, err)
		}
	}
	return currentVersion, nil
}

func runMigrations(m *migrate.Migrate, direction string) error {
	var err error
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

func lockMigrations(db *sql.DB) (*sql.Conn, error) {
	conn, err := db.Conn(context.Background())
	if err != nil {
		return nil, fmt.Errorf("open migration lock connection: %w", err)
	}
	if _, err := conn.ExecContext(context.Background(), `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		conn.Close()
		return nil, fmt.Errorf("acquire migration lock: %w", err)
	}
	return conn, nil
}

func unlockMigrations(conn *sql.Conn) error {
	_, unlockErr := conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockID)
	closeErr := conn.Close()
	if unlockErr != nil {
		return fmt.Errorf("release migration lock: %w", unlockErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close migration lock connection: %w", closeErr)
	}
	return nil
}

func legacyMigrationVersion(db *sql.DB) (uint, string, error) {
	var exists bool
	if err := db.QueryRow(`SELECT to_regclass('schema_migrations') IS NOT NULL`).Scan(&exists); err != nil {
		return 0, "", fmt.Errorf("check legacy migration ledger: %w", err)
	}
	if !exists {
		return 0, "", nil
	}
	var version int64
	var name string
	if err := db.QueryRow(`SELECT version, name FROM schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&version, &name); err != nil {
		if err == sql.ErrNoRows {
			return 0, "", nil
		}
		return 0, "", fmt.Errorf("read legacy migration version: %w", err)
	}
	if version < 0 {
		return 0, "", nil
	}
	return uint(version), name, nil
}

func validateMigrationSequence(driver source.Driver) (uint, error) {
	version, err := driver.First()
	if err != nil {
		return 0, fmt.Errorf("read first migration version: %w", err)
	}
	var latest uint
	for expected := uint(1); ; expected++ {
		if version != expected {
			return 0, fmt.Errorf("migration versions must be contiguous from 1: found %d, want %d", version, expected)
		}
		up, _, err := driver.ReadUp(version)
		if err != nil {
			return 0, fmt.Errorf("migration %d is missing its up file: %w", version, err)
		}
		if err := up.Close(); err != nil {
			return 0, fmt.Errorf("close migration %d up file: %w", version, err)
		}
		down, _, err := driver.ReadDown(version)
		if err != nil {
			return 0, fmt.Errorf("migration %d is missing its down file: %w", version, err)
		}
		if err := down.Close(); err != nil {
			return 0, fmt.Errorf("close migration %d down file: %w", version, err)
		}
		latest = version
		next, err := driver.Next(version)
		if errors.Is(err, fs.ErrNotExist) {
			return latest, nil
		}
		if err != nil {
			return 0, fmt.Errorf("read migration after version %d: %w", version, err)
		}
		version = next
	}
}

func migrationVersion(m *migrate.Migrate) (uint, bool, error) {
	version, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read migration version: %w", err)
	}
	return version, dirty, nil
}

func syncLegacyMigrationLedger(db *sql.DB, version uint, name string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin legacy migration ledger update: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`); err != nil {
		return fmt.Errorf("create legacy migration ledger: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM schema_migrations WHERE version > $1`, version); err != nil {
		return fmt.Errorf("remove newer legacy migration versions: %w", err)
	}
	if version > 0 {
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version, name) VALUES ($1, $2) ON CONFLICT (version) DO UPDATE SET name = EXCLUDED.name, applied_at = now()`, version, name); err != nil {
			return fmt.Errorf("record legacy migration version: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit legacy migration ledger update: %w", err)
	}
	return nil
}
