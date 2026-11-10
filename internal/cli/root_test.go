package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpIncludesServe(t *testing.T) {
	cmd := NewRootCommand(nil)
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(output.String(), "serve") {
		t.Fatalf("help does not mention serve: %s", output.String())
	}
}

func TestMigrateCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	config := writeConfig(t, fmt.Sprintf("database:\n  path: %q\n", path))
	for _, want := range []string{"Applied 3 migration(s).", "Database is already up to date."} {
		cmd := NewRootCommand(nil)
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetArgs([]string{"migrate", "--config", config})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output = %q, want %q", output.String(), want)
		}
	}
}

func TestMigrateRejectsArguments(t *testing.T) {
	cmd := NewRootCommand(nil)
	cmd.SetArgs([]string{"migrate", "unexpected"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error for an unexpected argument")
	}
}

func TestMigrateInvalidPath(t *testing.T) {
	cmd := NewRootCommand(nil)
	config := writeConfig(t, fmt.Sprintf("database:\n  path: %q\n", filepath.Join(t.TempDir(), "missing", "test.db")))
	cmd.SetArgs([]string{"migrate", "--config", config})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error for an invalid database path")
	}
}

func TestServeRejectsArguments(t *testing.T) {
	cmd := NewRootCommand(nil)
	cmd.SetArgs([]string{"serve", "unexpected"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error for an unexpected argument")
	}
}

func TestServeInvalidAddress(t *testing.T) {
	cmd := NewRootCommand(nil)
	config := writeConfig(t, "server:\n  addr: invalid-address\nface:\n  enabled: false\n")
	cmd.SetArgs([]string{"serve", "--config", config})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error for an invalid address")
	}
}

func writeConfig(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMissingExplicitConfig(t *testing.T) {
	cmd := NewRootCommand(nil)
	cmd.SetArgs([]string{"--config", filepath.Join(t.TempDir(), "missing.yaml"), "migrate"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "read configuration") {
		t.Fatalf("expected configuration error, got %v", err)
	}
}

func TestOldFlagsRejected(t *testing.T) {
	cmd := NewRootCommand(nil)
	cmd.SetArgs([]string{"migrate", "--db", "test.db"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected removed flag to be rejected")
	}
}
