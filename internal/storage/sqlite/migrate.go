package sqlite

import (
	_ "embed"
	"fmt"

	"go.uber.org/zap"
)

//go:embed sql/0001_initial_schema.sql
var migration0001 string

//go:embed sql/0002_v0_profile_foundation.sql
var migration0002 string

//go:embed sql/0003_v0_completion.sql
var migration0003 string

//go:embed sql/0004_v0_fts_sync.sql
var migration0004 string

type migration struct {
	version int
	name    string
	sql     string
}

func registeredMigrations() []migration {
	return []migration{
		{version: 1, name: "0001_initial_schema", sql: migration0001},
		{version: 2, name: "0002_v0_profile_foundation", sql: migration0002},
		{version: 3, name: "0003_v0_completion", sql: migration0003},
		{version: 4, name: "0004_v0_fts_sync", sql: migration0004},
	}
}

func (d *DB) migrate() error {
	_, err := d.db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT    NOT NULL,
			applied_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		)`)
	if err != nil {
		return fmt.Errorf("bootstrap schema_migrations: %w", err)
	}

	if _, err := d.db.Exec("PRAGMA journal_mode = WAL;"); err != nil {
		return fmt.Errorf("set journal_mode WAL: %w", err)
	}
	if _, err := d.db.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		return fmt.Errorf("enable foreign_keys: %w", err)
	}
	if _, err := d.db.Exec("PRAGMA busy_timeout = 5000;"); err != nil {
		return fmt.Errorf("set busy_timeout: %w", err)
	}

	applied, err := d.appliedVersions()
	if err != nil {
		return err
	}

	for _, m := range registeredMigrations() {
		if applied[m.version] {
			d.log.Debug("sqlite: migration already applied", zap.Int("version", m.version))
			continue
		}

		d.log.Info("sqlite: applying migration",
			zap.Int("version", m.version),
			zap.String("name", m.name),
		)

		tx, err := d.db.Begin()
		if err != nil {
			return fmt.Errorf("begin tx for migration %d: %w", m.version, err)
		}

		if _, err := tx.Exec(m.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("execute migration %d (%s): %w", m.version, m.name, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.version, err)
		}

		d.log.Info("sqlite: migration applied", zap.Int("version", m.version))
	}

	return nil
}

func (d *DB) appliedVersions() (map[int]bool, error) {
	rows, err := d.db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("query applied migrations: %w", err)
	}
	defer rows.Close()

	applied := map[int]bool{}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		applied[v] = true
	}
	return applied, rows.Err()
}
