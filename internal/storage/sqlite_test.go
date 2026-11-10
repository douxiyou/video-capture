package storage

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jmoiron/sqlx"
)

func openTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "test #1?.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

func TestMigrateAndConstraints(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	for i, want := range []int{3, 0} {
		got, err := Migrate(ctx, db)
		if err != nil || got != want {
			t.Fatalf("migration %d: got (%d, %v), want (%d, nil)", i, got, err, want)
		}
	}

	result, err := db.ExecContext(ctx, "INSERT INTO users (name) VALUES (?)", "测试用户")
	if err != nil {
		t.Fatal(err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(ctx, `INSERT INTO image_uploads
		(user_id, file_path, original_filename, content_type, size_bytes, expression)
		VALUES (?, ?, ?, ?, ?, ?)`, userID, "uploads/a.jpg", "a.jpg", "image/jpeg", 123, "微笑")
	if err != nil {
		t.Fatal(err)
	}
	var expression string
	if err := db.GetContext(ctx, &expression, "SELECT expression FROM image_uploads WHERE user_id = ?", userID); err != nil || expression != "微笑" {
		t.Fatalf("expression = %q, err = %v", expression, err)
	}
	for _, query := range []string{
		"INSERT INTO users (name) VALUES (' ')",
		"INSERT INTO image_uploads (user_id, file_path, original_filename, content_type, size_bytes) VALUES (999, 'a', 'a', 'image/jpeg', 1)",
		"INSERT INTO image_uploads (user_id, file_path, original_filename, content_type, size_bytes) VALUES (1, 'a', 'a', 'image/jpeg', -1)",
		"DELETE FROM users WHERE id = 1",
	} {
		if _, err := db.ExecContext(ctx, query); err == nil {
			t.Errorf("expected constraint failure for %s", query)
		}
	}
	var count int
	if err := db.GetContext(ctx, &count, "SELECT COUNT(*) FROM schema_migrations"); err != nil || count != 3 {
		t.Fatalf("migration records = %d, err = %v", count, err)
	}
}

func TestMigrationFailureRollsBack(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec("CREATE TABLE image_uploads (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(context.Background(), db); err == nil {
		t.Fatal("expected conflicting schema to fail")
	}
	var count int
	if err := db.Get(&count, "SELECT COUNT(*) FROM sqlite_master WHERE name IN ('schema_migrations', 'users')"); err != nil || count != 0 {
		t.Fatalf("migration left partial schema: count = %d, err = %v", count, err)
	}
}

func TestMigrationPreservesExistingUploads(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.Exec(migrations[0].sql); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(migrations[1].sql); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY); INSERT INTO schema_migrations VALUES (1), (2)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO users (name) VALUES ('old'); INSERT INTO image_uploads (user_id, file_path, original_filename, content_type, size_bytes) VALUES (1, 'old.png', 'old.png', 'image/png', 2)"); err != nil {
		t.Fatal(err)
	}
	count, err := Migrate(ctx, db)
	if err != nil || count != 1 {
		t.Fatalf("migration: %d, %v", count, err)
	}
	var status string
	if err := db.Get(&status, "SELECT status FROM image_uploads"); err != nil || status != "uploaded" {
		t.Fatalf("existing upload status: %q, %v", status, err)
	}
}

func TestForeignKeysOnEveryConnection(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	first, err := db.Connx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := db.Connx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	for _, conn := range []*sqlx.Conn{first, second} {
		var enabled int
		if err := conn.GetContext(ctx, &enabled, "PRAGMA foreign_keys"); err != nil || enabled != 1 {
			t.Fatalf("foreign_keys = %d, err = %v", enabled, err)
		}
	}
}

func TestConcurrentMigrations(t *testing.T) {
	db := openTestDB(t)
	const workers = 4
	var wg sync.WaitGroup
	type result struct {
		count int
		err   error
	}
	results := make(chan result, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			count, err := Migrate(context.Background(), db)
			results <- result{count, err}
		}()
	}
	wg.Wait()
	close(results)
	total := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		total += result.count
	}
	if total != 3 {
		t.Fatalf("applied %d migrations across concurrent calls, want 3", total)
	}
}

func TestOpenErrors(t *testing.T) {
	if _, err := Open(context.Background(), ""); err == nil {
		t.Fatal("expected empty path to fail")
	}
	if _, err := Open(context.Background(), filepath.Join(t.TempDir(), "missing", "db.sqlite")); err == nil {
		t.Fatal("expected missing parent directory to fail")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Open(ctx, filepath.Join(t.TempDir(), "cancelled.db")); err == nil {
		t.Fatal("expected canceled context to fail")
	}
}
