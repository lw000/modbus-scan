// Package store persists device and point configuration in SQLite.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var (
	// ErrNotFound indicates that the requested row does not exist.
	ErrNotFound = errors.New("not found")
	// ErrConflict indicates a uniqueness or state conflict.
	ErrConflict = errors.New("conflict")
)

// Store owns the SQLite connection.
type Store struct{ db *sql.DB }

// Open creates or opens a SQLite database and applies its schema.
func Open(ctx context.Context, path string, busyTimeout time.Duration) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		fmt.Sprintf("PRAGMA busy_timeout = %d", busyTimeout.Milliseconds()),
	}
	for _, statement := range pragmas {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("configure database: %w", err)
		}
	}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS devices (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    enabled INTEGER NOT NULL DEFAULT 0,
    address TEXT NOT NULL,
    port INTEGER NOT NULL,
    slave_id INTEGER NOT NULL,
    byte_order TEXT NOT NULL,
    timeout_sec INTEGER NOT NULL,
    scan_interval_ms INTEGER NOT NULL,
    config_version INTEGER NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS points (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    device_id INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    tag_name TEXT NOT NULL,
    reg_type TEXT NOT NULL,
    address INTEGER NOT NULL,
    data_type TEXT NOT NULL,
    bit_offset INTEGER NOT NULL,
    bit_len INTEGER NOT NULL,
    writeable INTEGER NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE(device_id, tag_name)
);
CREATE INDEX IF NOT EXISTS idx_points_device_address ON points(device_id, reg_type, address, tag_name);
INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES(1, CURRENT_TIMESTAMP);`
	if _, err := s.db.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	return s.migratePointDescription(ctx)
}

func (s *Store) migratePointDescription(ctx context.Context) error {
	var applied int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE version=2`).Scan(&applied); err != nil {
		return fmt.Errorf("check migration 2: %w", err)
	}
	if applied != 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration 2: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `ALTER TABLE points ADD COLUMN description TEXT NOT NULL DEFAULT ''`); err != nil {
		return fmt.Errorf("add point description: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations(version, applied_at) VALUES(2, CURRENT_TIMESTAMP)`); err != nil {
		return fmt.Errorf("record migration 2: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration 2: %w", err)
	}
	return nil
}

// Close closes the SQLite connection.
func (s *Store) Close() error { return s.db.Close() }

func mapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%s: %w", operation, ErrNotFound)
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "unique constraint") || strings.Contains(text, "constraint failed") {
		return fmt.Errorf("%s: %w", operation, ErrConflict)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
