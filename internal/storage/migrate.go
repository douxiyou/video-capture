package storage

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
)

type migration struct {
	version int
	sql     string
}

var migrations = []migration{
	{
		version: 1,
		sql: `
CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE TABLE image_uploads (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    file_path TEXT NOT NULL CHECK (length(trim(file_path)) > 0),
    original_filename TEXT NOT NULL,
    content_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
    expression TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);

CREATE INDEX idx_image_uploads_user_id ON image_uploads(user_id);
`,
	},
	{
		version: 2,
		sql: `
CREATE UNIQUE INDEX idx_users_name ON users(name);
ALTER TABLE users ADD COLUMN credential_id TEXT;
CREATE UNIQUE INDEX idx_users_credential_id ON users(credential_id);
CREATE TABLE face_embeddings (
    user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    model_id TEXT NOT NULL,
    encrypted_embedding BLOB NOT NULL,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
`,
	},
	{
		version: 3,
		sql: `
ALTER TABLE image_uploads ADD COLUMN status TEXT NOT NULL DEFAULT 'uploaded' CHECK (status IN ('pending', 'uploaded'));
`,
	},
}

// Migrate applies pending migrations atomically and returns the number applied.
func Migrate(ctx context.Context, db *sqlx.DB) (int, error) {
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin database migration: %w", err)
	}
	defer tx.Rollback()

	// Writing before reading the version table serializes concurrent migrations.
	if _, err := tx.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
)`); err != nil {
		return 0, fmt.Errorf("create migration table: %w", err)
	}

	applied := 0
	for _, m := range migrations {
		var count int
		if err := tx.GetContext(ctx, &count, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", m.version); err != nil {
			return 0, fmt.Errorf("check migration %d: %w", m.version, err)
		}
		if count != 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			return 0, fmt.Errorf("apply migration %d: %w", m.version, err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO schema_migrations (version) VALUES (?)", m.version); err != nil {
			return 0, fmt.Errorf("record migration %d: %w", m.version, err)
		}
		applied++
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit database migration: %w", err)
	}
	return applied, nil
}
