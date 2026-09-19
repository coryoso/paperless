package fm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeCLI(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fm"), []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestAvailable(t *testing.T) {
	for _, tt := range []struct {
		output string
		wantOK bool
	}{
		{"System model available", true},
		{"System model unavailable: Apple Intelligence is disabled", false},
	} {
		t.Run(tt.output, func(t *testing.T) {
			fakeCLI(t, "printf '%s\\n' \"$FM_TEST_OUTPUT\"")
			t.Setenv("FM_TEST_OUTPUT", tt.output)
			if err := Available(t.Context()); (err == nil) != tt.wantOK {
				t.Fatalf("Available() = %v", err)
			}
		})
	}
}

func TestMissingCLI(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := Available(t.Context()); err == nil || !strings.Contains(err.Error(), "fm available") {
		t.Fatalf("error = %v", err)
	}
}

func TestRespondUsesStdinAndPrivateTemporarySchema(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FM_TEST_DIR", dir)
	fakeCLI(t, `
printf '%s\n' "$@" > "$FM_TEST_DIR/args"
cat > "$FM_TEST_DIR/input"
while [ "$#" -gt 0 ]; do
  if [ "$1" = --schema ]; then
    shift
    printf '%s' "$1" > "$FM_TEST_DIR/schema-path"
    cp "$1" "$FM_TEST_DIR/schema"
  fi
  shift
done
printf '%s' '{"sender":"REWE"}'
`)
	prompt := "private document: $(touch unexpected); Grüße"
	schema := []byte(`{"type":"object"}`)
	output, err := Respond(t.Context(), "Extract fields", prompt, schema)
	if err != nil || output != `{"sender":"REWE"}` {
		t.Fatalf("Respond = %q, %v", output, err)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	if strings.Contains(string(args), prompt) || !strings.Contains(string(args), "--no-stream") || !strings.Contains(string(args), "system") {
		t.Fatalf("unexpected arguments: %s", args)
	}
	input, _ := os.ReadFile(filepath.Join(dir, "input"))
	if string(input) != prompt {
		t.Fatalf("stdin = %q", input)
	}
	copiedSchema, _ := os.ReadFile(filepath.Join(dir, "schema"))
	if string(copiedSchema) != string(schema) {
		t.Fatalf("schema = %q", copiedSchema)
	}
	schemaPath, _ := os.ReadFile(filepath.Join(dir, "schema-path"))
	if _, err := os.Stat(string(schemaPath)); !os.IsNotExist(err) {
		t.Fatalf("temporary schema still exists: %v", err)
	}
}

func TestCommandFailureAndCancellation(t *testing.T) {
	fakeCLI(t, "echo 'model unavailable' >&2; exit 1")
	if _, err := Respond(t.Context(), "", "", nil); err == nil || !strings.Contains(err.Error(), "model unavailable") {
		t.Fatalf("error = %v", err)
	}
	fakeCLI(t, "exec sleep 30")
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := CountTokens(ctx, "", ""); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
}

func TestCountTokensRejectsInvalidOutput(t *testing.T) {
	fakeCLI(t, "echo invalid")
	if _, err := CountTokens(t.Context(), "", ""); err == nil {
		t.Fatal("expected invalid token count error")
	}
}
