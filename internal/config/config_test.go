package config

import (
	"os"
	"path/filepath"
	"testing"
)

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
