package db

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// Store wraps a *sql.DB connection to the SQLite database.
type Store struct {
	db *sql.DB
}

// Open creates a new Store by opening the SQLite database at the given path.
// It configures pragmas for performance and reliability, then runs migrations.
func Open(path string) (*Store, error) {
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// SQLite performs best with a single connection to avoid SQLITE_BUSY.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	sqlDB.SetConnMaxLifetime(0)

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
		"PRAGMA cache_size=-20000",
		"PRAGMA auto_vacuum=INCREMENTAL",
		"PRAGMA temp_store=MEMORY",
	}
	for _, p := range pragmas {
		if _, err := sqlDB.Exec(p); err != nil {
			sqlDB.Close()
			return nil, fmt.Errorf("set pragma %q: %w", p, err)
		}
	}

	s := &Store{db: sqlDB}
	if err := s.migrate(); err != nil {
		sqlDB.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return s, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB returns the underlying *sql.DB for use in tests or advanced scenarios.
func (s *Store) DB() *sql.DB {
	return s.db
}

// migrate runs version-based schema migrations using PRAGMA user_version.
func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}

	migrations := []func(tx *sql.Tx) error{
		s.migrateV1,
		s.migrateV2,
	}

	for i := version; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration v%d: %w", i+1, err)
		}
		if err := migrations[i](tx); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration v%d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return fmt.Errorf("set user_version to %d: %w", i+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration v%d: %w", i+1, err)
		}
	}

	return nil
}

// migrateV2 adds the trace_id column for download pipeline tracing.
func (s *Store) migrateV2(tx *sql.Tx) error {
	_, err := tx.Exec(`ALTER TABLE downloads ADD COLUMN trace_id TEXT NOT NULL DEFAULT ''`)
	return err
}

// migrateV1 creates the initial database schema.
func (s *Store) migrateV1(tx *sql.Tx) error {
	stmts := []string{
		`CREATE TABLE downloads (
			id            TEXT PRIMARY KEY,
			url           TEXT NOT NULL,
			filename      TEXT NOT NULL,
			dir           TEXT NOT NULL,
			total_size    INTEGER DEFAULT -1,
			downloaded    INTEGER DEFAULT 0,
			status        TEXT DEFAULT 'queued',
			segments      INTEGER DEFAULT 16,
			speed_limit   INTEGER DEFAULT 0,
			headers       TEXT,
			referer_url   TEXT DEFAULT '',
			checksum      TEXT DEFAULT '',
			etag          TEXT DEFAULT '',
			last_modified TEXT DEFAULT '',
			error         TEXT DEFAULT '',
			created_at    TEXT DEFAULT (datetime('now')),
			completed_at  TEXT,
			scheduled_at  TEXT,
			queue_order   INTEGER DEFAULT 0
		)`,
		`CREATE TABLE segments (
			download_id TEXT    NOT NULL,
			idx         INTEGER NOT NULL,
			start_byte  INTEGER NOT NULL,
			end_byte    INTEGER NOT NULL,
			downloaded  INTEGER DEFAULT 0,
			done        INTEGER DEFAULT 0,
			PRIMARY KEY (download_id, idx),
			FOREIGN KEY (download_id) REFERENCES downloads(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX idx_downloads_status  ON downloads(status)`,
		`CREATE INDEX idx_downloads_created ON downloads(created_at DESC)`,
		`CREATE INDEX idx_downloads_queue   ON downloads(queue_order ASC)`,
	}
	for _, stmt := range stmts {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("exec %q: %w", stmt[:40], err)
		}
	}
	return nil
}
