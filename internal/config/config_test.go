package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultRequiresDocumentsDirectorySelection(t *testing.T) {
	if got := Default().Paths.ArchiveRoot; got != "" {
		t.Fatalf("archive root = %q, want empty until setup", got)
	}
}

func TestDashboardURLUsesConfiguredAddress(t *testing.T) {
	cfg := Default()
	if got := cfg.DashboardURL(); got != "http://127.0.0.1:8844" {
		t.Fatalf("default dashboard URL = %q", got)
	}
	cfg.Service.Host = "localhost"
	cfg.Service.Port = 9988
	if got := cfg.DashboardURL(); got != "http://localhost:9988" {
		t.Fatalf("configured dashboard URL = %q", got)
	}
	cfg.Service.Host = "::"
	if got := cfg.DashboardURL(); got != "http://127.0.0.1:9988" {
		t.Fatalf("wildcard dashboard URL = %q", got)
	}
}

func TestResolveRejectsInvalidServicePort(t *testing.T) {
	cfg := Default()
	cfg.Service.Port = 65536
	if _, err := cfg.Resolve(); err == nil || !strings.Contains(err.Error(), "service port") {
		t.Fatalf("Resolve() error = %v, want invalid service port", err)
	}
}

func TestWriteAtomicallyPersistsPrivateConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.toml")
	cfg := Default()
	cfg.Paths.ArchiveRoot = filepath.Join(t.TempDir(), "documents")
	if _, err := Write(path, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "archive_root") {
		t.Fatalf("config missing archive_root: %s", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}
}

func TestEnsureDirsCreatesConfiguredInbox(t *testing.T) {
	base := t.TempDir()
	cfg := Default()
	cfg.Paths.Inbox = filepath.Join(base, "scanner-inbox")
	cfg.Paths.Raw = filepath.Join(base, "raw")
	cfg.Paths.Processing = filepath.Join(base, "processing")
	cfg.Paths.Archive = filepath.Join(base, "archive")
	cfg.Paths.Review = filepath.Join(base, "review")
	cfg.Paths.Rejected = filepath.Join(base, "rejected")
	cfg.Paths.Duplicates = filepath.Join(base, "duplicates")
	cfg.Paths.Logs = filepath.Join(base, "logs")
	cfg.Paths.StateDir = filepath.Join(base, "state")

	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(cfg.Paths.Inbox); err != nil || !info.IsDir() {
		t.Fatalf("inbox was not created: info=%v err=%v", info, err)
	}
}

func TestDefaultUsesNineBModelSettings(t *testing.T) {
	cfg := Default()
	if got := cfg.LLM.Model; got != "qwen3.5:9b-q4_K_M" {
		t.Fatalf("model = %q", got)
	}
	if got := cfg.LLM.TimeoutSeconds; got != 360 {
		t.Fatalf("timeout = %d", got)
	}
	if got := cfg.LLM.ContextTokens; got != 16_384 {
		t.Fatalf("context tokens = %d", got)
	}
	if got := cfg.LLM.ReasoningTokens; got != 2_048 {
		t.Fatalf("reasoning tokens = %d", got)
	}
	if got := cfg.LLM.MaxOutputTokens; got != 2_048 {
		t.Fatalf("max output tokens = %d", got)
	}
	if got := cfg.LLM.KeepAlive; got != "0" {
		t.Fatalf("keep alive = %q", got)
	}
}

func TestLoadFMProviderOverridesInheritedOllamaModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[llm]\nprovider = 'fm'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Provider != "fm" || cfg.LLM.Model != "system" {
		t.Fatalf("fm config = %+v", cfg.LLM)
	}
	cfg.LLM.Provider = "ollama"
	cfg, err = cfg.Resolve()
	if err != nil || cfg.LLM.Model != Default().LLM.Model {
		t.Fatalf("switch back to Ollama = %+v, %v", cfg.LLM, err)
	}
}

func TestResolveRejectsUnknownModelProvider(t *testing.T) {
	cfg := Default()
	cfg.LLM.Provider = "typo"
	if _, err := cfg.Resolve(); err == nil {
		t.Fatal("expected unknown provider error")
	}
}
