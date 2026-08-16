package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/alesr/pocketPDS/internal/crypto"
	_ "modernc.org/sqlite"
)

type Store struct {
	DB  *sql.DB
	Box *crypto.Box
}

func Open(ctx context.Context, path, secret string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	s := &Store{DB: db, Box: crypto.NewBox(secret)}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

// migration is an ordered, idempotent schema step.
type migration struct {
	version int
	name    string
	stmts   []string
}

var migrations = []migration{
	{
		version: 1,
		name:    "initial",
		stmts: []string{
			`CREATE TABLE IF NOT EXISTS accounts (
				did TEXT PRIMARY KEY,
				handle TEXT NOT NULL UNIQUE,
				email TEXT,
				password_hash TEXT NOT NULL,
				recovery_key TEXT NOT NULL,
				signing_key TEXT NOT NULL,
				tid_clock INTEGER NOT NULL DEFAULT 0,
				created_at TEXT NOT NULL,
				did_doc TEXT,
				deactivated_at TEXT,
				takedown_ref TEXT)`,
			`CREATE INDEX IF NOT EXISTS idx_accounts_handle ON accounts(handle)`,
			`CREATE TABLE IF NOT EXISTS auth_sessions (
				token_hash TEXT PRIMARY KEY,
				did TEXT NOT NULL REFERENCES accounts(did) ON DELETE CASCADE,
				refresh_token TEXT NOT NULL,
				created_at TEXT NOT NULL,
				expires_at TEXT NOT NULL,
				app_password TEXT)`,
			`CREATE INDEX IF NOT EXISTS idx_auth_sessions_did ON auth_sessions(did)`,
			`CREATE TABLE IF NOT EXISTS repo_commits (
				did TEXT NOT NULL,
				rev TEXT NOT NULL,
				cid BLOB NOT NULL,
				prev_cid BLOB,
				data_root BLOB NOT NULL,
				sig BLOB NOT NULL,
				created_at TEXT NOT NULL,
				PRIMARY KEY (did, rev))`,
			`CREATE INDEX IF NOT EXISTS idx_repo_commits_did ON repo_commits(did, created_at DESC)`,
			`CREATE TABLE IF NOT EXISTS repo_blocks (cid BLOB PRIMARY KEY, data BLOB NOT NULL, size INTEGER NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS repo_records (
				did TEXT NOT NULL,
				collection TEXT NOT NULL,
				rkey TEXT NOT NULL,
				record_cid BLOB NOT NULL,
				value BLOB NOT NULL,
				PRIMARY KEY (did, collection, rkey))`,
			`CREATE INDEX IF NOT EXISTS idx_repo_records_col ON repo_records(did, collection, rkey)`,
			`CREATE TABLE IF NOT EXISTS repo_block_revs (
				did TEXT NOT NULL,
				cid BLOB NOT NULL,
				rev TEXT NOT NULL,
				PRIMARY KEY (did, cid))`,
			`CREATE TABLE IF NOT EXISTS blobs (
				cid BLOB PRIMARY KEY,
				did TEXT NOT NULL REFERENCES accounts(did) ON DELETE CASCADE,
				size INTEGER NOT NULL,
				mime_type TEXT,
				storage TEXT NOT NULL,
				path TEXT,
				data BLOB,
				created_at TEXT NOT NULL)`,
			`CREATE INDEX IF NOT EXISTS idx_blobs_did ON blobs(did)`,
			`CREATE TABLE IF NOT EXISTS firehose_events (
				seq INTEGER PRIMARY KEY,
				frame BLOB NOT NULL)`,
		},
	},
	{
		version: 2,
		name:    "account lifecycle",
		stmts: []string{
			`ALTER TABLE accounts ADD COLUMN email_confirmed_at TEXT`,
			`CREATE TABLE IF NOT EXISTS app_passwords (
				did TEXT NOT NULL REFERENCES accounts(did) ON DELETE CASCADE,
				name TEXT NOT NULL,
				password_hash TEXT NOT NULL,
				created_at TEXT NOT NULL,
				PRIMARY KEY (did, name))`,
			`CREATE TABLE IF NOT EXISTS invite_codes (
				code TEXT PRIMARY KEY,
				created_by TEXT NOT NULL,
				created_at TEXT NOT NULL,
				used_by TEXT,
				used_at TEXT,
				disabled_at TEXT)`,
			`CREATE TABLE IF NOT EXISTS email_tokens (
				token TEXT PRIMARY KEY,
				did TEXT NOT NULL,
				purpose TEXT NOT NULL,
				email TEXT NOT NULL,
				expires_at TEXT NOT NULL,
				used_at TEXT)`,
		},
	},
	{
		version: 3,
		name:    "relay registry",
		stmts: []string{
			`CREATE TABLE IF NOT EXISTS relays (
				hostname TEXT PRIMARY KEY,
				registered_at TEXT NOT NULL)`,
		},
	},
	{
		version: 4,
		name:    "settings",
		stmts: []string{
			`CREATE TABLE IF NOT EXISTS settings (
				key TEXT PRIMARY KEY,
				value TEXT NOT NULL)`,
		},
	},
	{
		version: 5,
		name:    "appview preferences",
		stmts: []string{
			`CREATE TABLE IF NOT EXISTS app_preferences (
				did TEXT PRIMARY KEY REFERENCES accounts(did) ON DELETE CASCADE,
				prefs BLOB NOT NULL)`,
		},
	},
	{
		version: 6,
		name:    "appview mutes",
		stmts: []string{
			`CREATE TABLE IF NOT EXISTS mutes (
				did TEXT NOT NULL REFERENCES accounts(did) ON DELETE CASCADE,
				subject TEXT NOT NULL,
				created_at TEXT NOT NULL,
				PRIMARY KEY (did, subject))`,
		},
	},
	{
		version: 7,
		name:    "bridge",
		stmts: []string{
			`CREATE TABLE IF NOT EXISTS bridge_config (
				key TEXT PRIMARY KEY,
				value TEXT NOT NULL)`,
			`CREATE TABLE IF NOT EXISTS bridge_sync (
				direction TEXT NOT NULL,
				source_cid TEXT NOT NULL,
				local_uri TEXT,
				remote_uri TEXT,
				synced_at TEXT NOT NULL,
				PRIMARY KEY (direction, source_cid))`,
		},
	},
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.DB.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	var current int
	if err := s.DB.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := s.apply(ctx, m); err != nil {
			return fmt.Errorf("migration %d (%s): %w", m.version, m.name, err)
		}
	}
	return nil
}

func (s *Store) apply(ctx context.Context, m migration) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, stmt := range m.stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, datetime('now'))",
		m.version, m.name); err != nil {
		return err
	}
	return tx.Commit()
}
