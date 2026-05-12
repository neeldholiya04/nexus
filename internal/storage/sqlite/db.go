package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	_ "modernc.org/sqlite"
)

var errNoRows = sql.ErrNoRows

type DB struct {
	db  *sql.DB
	log *zap.Logger
}

type Config struct {
	Path          string
	MaxOpenConns  int
	BusyTimeoutMs int
}

func New(cfg Config, log *zap.Logger) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o750); err != nil {
		return nil, fmt.Errorf("sqlite: create data dir: %w", err)
	}

	dsn := fmt.Sprintf(
		"file:%s?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=%d",
		cfg.Path, cfg.BusyTimeoutMs,
	)

	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: open: %w", err)
	}

	maxConns := cfg.MaxOpenConns
	if maxConns <= 0 {
		maxConns = 1
	}
	sqlDB.SetMaxOpenConns(maxConns)
	sqlDB.SetMaxIdleConns(maxConns)
	sqlDB.SetConnMaxLifetime(0)

	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("sqlite: ping: %w", err)
	}

	store := &DB{db: sqlDB, log: log}

	if err := store.migrate(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("sqlite: migrate: %w", err)
	}

	log.Info("sqlite: ready", zap.String("path", cfg.Path))
	return store, nil
}

func (d *DB) Close() error {
	if err := d.db.Close(); err != nil && !errors.Is(err, sql.ErrConnDone) {
		return fmt.Errorf("sqlite: close: %w", err)
	}
	return nil
}
