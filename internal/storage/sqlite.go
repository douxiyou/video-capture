package storage

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// Open opens a SQLite database with foreign keys enabled on every connection.
func Open(ctx context.Context, path string) (*sqlx.DB, error) {
	if path == "" {
		return nil, fmt.Errorf("database path must not be empty")
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(absolutePath)}
	params := url.Values{}
	params.Add("_pragma", "foreign_keys(1)")
	params.Add("_pragma", "busy_timeout(5000)")
	params.Add("_pragma", "secure_delete(1)")
	u.RawQuery = params.Encode()

	db, err := sqlx.ConnectContext(ctx, "sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("open SQLite database %q: %w", path, err)
	}
	return db, nil
}
