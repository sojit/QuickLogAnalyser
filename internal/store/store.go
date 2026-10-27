package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// SchemaVersion is the latest applied schema. Never edit an already-applied
// migration in place; bump and add forward-only changes.
const SchemaVersion = 6

type Store struct {
	DB *sql.DB
}

func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	s := &Store{DB: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	if s == nil || s.DB == nil {
		return nil
	}
	return s.DB.Close()
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_utc TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			original_name TEXT NOT NULL,
			sha256 TEXT,
			size_bytes INTEGER,
			state TEXT NOT NULL,
			stage TEXT NOT NULL,
			partial INTEGER NOT NULL DEFAULT 0,
			error_summary TEXT,
			created_utc TEXT NOT NULL,
			updated_utc TEXT NOT NULL,
			coverage_json TEXT,
			product_label TEXT,
			app_version TEXT,
			rule_version TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS sources (
			id TEXT PRIMARY KEY,
			job_id TEXT NOT NULL,
			parent_id TEXT,
			raw_path TEXT NOT NULL,
			kind TEXT NOT NULL,
			cache_path TEXT,
			line_count INTEGER DEFAULT 0,
			coverage TEXT NOT NULL,
			size_bytes INTEGER DEFAULT 0,
			FOREIGN KEY(job_id) REFERENCES jobs(id)
		)`,
		`CREATE TABLE IF NOT EXISTS attributes (
			id TEXT PRIMARY KEY,
			job_id TEXT NOT NULL,
			field_key TEXT NOT NULL,
			raw_value TEXT,
			normalized TEXT,
			confidence TEXT,
			status TEXT,
			source_id TEXT,
			line_start INTEGER,
			line_end INTEGER,
			selected INTEGER DEFAULT 1,
			scope TEXT,
			FOREIGN KEY(job_id) REFERENCES jobs(id)
		)`,
		`CREATE TABLE IF NOT EXISTS events (
			id TEXT PRIMARY KEY,
			job_id TEXT NOT NULL,
			source_id TEXT NOT NULL,
			severity TEXT NOT NULL,
			categories TEXT,
			message TEXT NOT NULL,
			line_start INTEGER NOT NULL,
			line_end INTEGER NOT NULL,
			raw_time TEXT,
			rule_ids TEXT,
			fingerprint TEXT,
			FOREIGN KEY(job_id) REFERENCES jobs(id)
		)`,
		`CREATE TABLE IF NOT EXISTS coverage_issues (
			id TEXT PRIMARY KEY,
			job_id TEXT NOT NULL,
			source_id TEXT,
			code TEXT NOT NULL,
			severity TEXT,
			reason TEXT NOT NULL,
			FOREIGN KEY(job_id) REFERENCES jobs(id)
		)`,
		`CREATE TABLE IF NOT EXISTS searches (
			id TEXT PRIMARY KEY,
			job_id TEXT NOT NULL,
			source_id TEXT NOT NULL,
			query TEXT NOT NULL,
			case_sensitive INTEGER NOT NULL,
			state TEXT NOT NULL,
			matching_lines INTEGER NOT NULL DEFAULT 0,
			occurrences INTEGER NOT NULL DEFAULT 0,
			count_final INTEGER NOT NULL DEFAULT 0,
			scanned_bytes INTEGER NOT NULL DEFAULT 0,
			scope_complete INTEGER NOT NULL DEFAULT 0,
			error TEXT,
			FOREIGN KEY(job_id) REFERENCES jobs(id)
		)`,
		`CREATE TABLE IF NOT EXISTS search_hits (
			search_id TEXT NOT NULL,
			line_no INTEGER NOT NULL,
			occurrences INTEGER NOT NULL,
			line_text TEXT NOT NULL,
			PRIMARY KEY(search_id, line_no)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_sources_job ON sources(job_id)`,
		`CREATE INDEX IF NOT EXISTS idx_events_job_src ON events(job_id, source_id)`,
		`CREATE INDEX IF NOT EXISTS idx_attrs_job ON attributes(job_id)`,
	}
	for _, q := range stmts {
		if _, err := s.DB.Exec(q); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	// Forward-only additive columns (safe on existing DBs; ignore duplicate-column).
	for _, q := range []string{
		`ALTER TABLE jobs ADD COLUMN progress_message TEXT`,
		`ALTER TABLE jobs ADD COLUMN progress_current TEXT`,
		`ALTER TABLE jobs ADD COLUMN files_done INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE jobs ADD COLUMN bytes_done INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE jobs ADD COLUMN sources_total INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE jobs ADD COLUMN parse_done INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE jobs ADD COLUMN elapsed_ms INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE jobs ADD COLUMN schema_flags TEXT`,
		`ALTER TABLE jobs ADD COLUMN error_detail TEXT`,
		`ALTER TABLE jobs ADD COLUMN collection_time_raw TEXT`,
		`ALTER TABLE jobs ADD COLUMN observed_log_start TEXT`,
		`ALTER TABLE jobs ADD COLUMN observed_log_end TEXT`,
		`ALTER TABLE sources ADD COLUMN source_kind TEXT`,
		`ALTER TABLE sources ADD COLUMN classify_reason TEXT`,
		`ALTER TABLE sources ADD COLUMN classify_confidence TEXT`,
		`ALTER TABLE sources ADD COLUMN classify_version TEXT`,
		`ALTER TABLE sources ADD COLUMN mtime_utc TEXT`,
		`ALTER TABLE sources ADD COLUMN first_time_raw TEXT`,
		`ALTER TABLE sources ADD COLUMN last_time_raw TEXT`,
		`ALTER TABLE events ADD COLUMN event_time_utc TEXT`,
		`ALTER TABLE events ADD COLUMN time_precision TEXT`,
		`ALTER TABLE events ADD COLUMN time_confidence TEXT`,
		`ALTER TABLE events ADD COLUMN time_zone_known INTEGER NOT NULL DEFAULT 0`,
		`CREATE INDEX IF NOT EXISTS idx_events_job_time ON events(job_id, event_time_utc)`,
		`CREATE INDEX IF NOT EXISTS idx_events_job_sev_time ON events(job_id, severity, event_time_utc)`,
		`CREATE TABLE IF NOT EXISTS line_checkpoints (
			source_id TEXT NOT NULL,
			line_no INTEGER NOT NULL,
			byte_offset INTEGER NOT NULL,
			PRIMARY KEY(source_id, line_no)
		)`,
		`ALTER TABLE jobs ADD COLUMN events_committed INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE jobs ADD COLUMN first_result_ms INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE jobs ADD COLUMN result_generation INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE jobs ADD COLUMN counts_final INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sources ADD COLUMN content_sha256 TEXT`,
		`ALTER TABLE sources ADD COLUMN content_bytes INTEGER NOT NULL DEFAULT 0`,
	} {
		_, _ = s.DB.Exec(q)
	}
	for v := 1; v <= SchemaVersion; v++ {
		var n int
		_ = s.DB.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, v).Scan(&n)
		if n == 0 {
			if _, err := s.DB.Exec(`INSERT INTO schema_migrations(version, applied_utc) VALUES (?, ?)`,
				v, time.Now().UTC().Format(time.RFC3339)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) WithTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// NeedsClassification reports whether any source lacks a semantic source_kind
// or was classified with a different classifier version.
func (s *Store) NeedsClassification(jobID, activeVersion string) (bool, error) {
	var n int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM sources WHERE job_id=? AND (source_kind IS NULL OR source_kind='' OR classify_version IS NULL OR classify_version='' OR classify_version!=?)`,
		jobID, activeVersion).Scan(&n)
	return n > 0, err
}
