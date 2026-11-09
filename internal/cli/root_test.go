package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestHelpIncludesServe(t *testing.T) {
	cmd := NewRootCommand()
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

func TestServeRejectsArguments(t *testing.T) {
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"serve", "unexpected"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error for an unexpected argument")
	}
}

func TestServeInvalidAddress(t *testing.T) {
	cmd := NewRootCommand()
	cmd.SetArgs([]string{"serve", "--addr", "invalid-address"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected an error for an invalid address")
	}
}
