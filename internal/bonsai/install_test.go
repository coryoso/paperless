package bonsai

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"howett.net/plist"
	"paperless/internal/config"
)

func TestServiceBindsLocallyAndKeepsModelIdentity(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.ContextTokens = 8192
	data, err := servicePlist(cfg, "/Local Data/Bonsai", "/Users/example")
	if err != nil {
		t.Fatal(err)
	}
	var service struct {
		Label                string
		ProgramArguments     []string
		EnvironmentVariables map[string]string
		KeepAlive            bool
	}
	if _, err := plist.Unmarshal(data, &service); err != nil {
		t.Fatal(err)
	}
	if service.Label != serviceLabel || !service.KeepAlive {
		t.Fatalf("service=%+v", service)
	}
	if strings.Join(service.ProgramArguments, "|") != "/bin/sh|/Local Data/Bonsai/scripts/start_llama_server.sh|--alias|Bonsai-8B|--parallel|1|--no-jinja" {
		t.Fatalf("args=%v", service.ProgramArguments)
	}
	for k, v := range map[string]string{"BONSAI_FAMILY": "bonsai", "BONSAI_MODEL": "8B", "BONSAI_HOST": "127.0.0.1", "BONSAI_CTX": "8192", "PORT": "8080"} {
		if service.EnvironmentVariables[k] != v {
			t.Fatalf("%s=%s", k, service.EnvironmentVariables[k])
		}
	}
}

func TestVerifyDownloadRejectsIncompleteOrChangedWeights(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.partial")
	content := []byte("complete model")
	hash := sha256.Sum256(content)
	want := hex.EncodeToString(hash[:])
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifySHA256(path, want); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content[:4], 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifySHA256(path, want); err == nil {
		t.Fatal("accepted partial model")
	}
}

func TestInstallLocksReleaseAfterFailure(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("unix lock")
	}
	dir := t.TempDir()
	unlock, err := lockInstall(dir)
	if err != nil {
		t.Fatal(err)
	}
	if another, err := lockInstall(dir); err == nil {
		another()
		t.Fatal("concurrent install accepted")
	}
	unlock()
	unlock, err = lockInstall(dir)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}

func TestInstallRejectsCustomServerBeforeRunningCommands(t *testing.T) {
	cfg := config.Default()
	cfg.Bonsai.Endpoint = "http://127.0.0.1:9999"
	cfg.Paths.StateDir = t.TempDir()
	if err := Install(t.Context(), cfg, io.Discard, io.Discard, nil); err == nil {
		t.Fatal("expected managed install rejection")
	}
	entries, _ := os.ReadDir(cfg.Paths.StateDir)
	if len(entries) != 0 {
		t.Fatal("custom configuration triggered install")
	}
}

func TestInstallStopsWhenGitFails(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS installer")
	}
	dir := t.TempDir()
	for _, name := range []string{"git", "curl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 7\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	cfg := config.Default()
	cfg.Paths.StateDir = t.TempDir()
	err := Install(t.Context(), cfg, io.Discard, io.Discard, nil)
	if err == nil || !strings.Contains(err.Error(), "git failed") {
		t.Fatalf("err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.Paths.StateDir, "bonsai", ".paperless-revision")); !os.IsNotExist(err) {
		t.Fatal("failed checkout was marked complete")
	}
}

func TestInstallerCancellationStopsShellChildren(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("unix processes")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", "sleep 30 & wait")
	configureInstallCommand(cmd)
	start := time.Now()
	if err := cmd.Run(); err == nil {
		t.Fatal("expected cancellation")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("child process survived cancellation")
	}
}
