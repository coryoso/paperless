package bonsai

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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
	if service.ProgramArguments[0] != "/Local Data/Bonsai/bin/mac/llama-server" {
		t.Fatalf("args=%v", service.ProgramArguments)
	}
	for flag, want := range map[string]string{
		"-m":     "/Local Data/Bonsai/models/gguf/8B/" + modelFilename,
		"--host": "127.0.0.1", "--port": "8080", "-c": "8192",
		"--alias": "Bonsai-8B", "--parallel": "1", "--reasoning-budget": "0",
		"--reasoning-format": "none", "--chat-template-kwargs": `{"enable_thinking": false}`,
	} {
		if got := argumentValue(service.ProgramArguments, flag); got != want {
			t.Fatalf("%s=%q, want %q", flag, got, want)
		}
	}
	if service.ProgramArguments[len(service.ProgramArguments)-1] != "--no-jinja" {
		t.Fatal("missing native JSON template flag")
	}
}

func argumentValue(args []string, flag string) string {
	for i := 1; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

func TestBootoutIgnoresOnlyMissingService(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture")
	}
	for _, tt := range []struct {
		name, script string
		wantError    bool
	}{
		{"stopped", "exit 0", false},
		{"not installed", "echo 'Boot-out failed: 3: No such process' >&2; exit 3", false},
		{"permission denied", "echo 'permission denied' >&2; exit 1", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "launchctl"), []byte("#!/bin/sh\n"+tt.script+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			err := bootoutService(t.Context(), "gui/123")
			if (err != nil) != tt.wantError {
				t.Fatalf("bootout error = %v", err)
			}
			if tt.wantError && !strings.Contains(err.Error(), "permission denied") {
				t.Fatalf("lost launchctl diagnostic: %v", err)
			}
		})
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
	cfg.Bonsai.Endpoint = "http://192.0.2.1:9999"
	cfg.Paths.StateDir = t.TempDir()
	if _, err := Install(t.Context(), cfg, io.Discard, io.Discard, nil); err == nil {
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
	_, err := Install(t.Context(), cfg, io.Discard, io.Discard, nil)
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

func TestPortSelectionPreservesFreePortAndAvoidsOccupiedPort(t *testing.T) {
	occupied, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	cfg := config.Default()
	cfg.Bonsai.Endpoint = "http://" + occupied.Addr().String()
	selected, err := reservePort(cfg.Bonsai)
	if err != nil {
		t.Fatal(err)
	}
	defer selected.Close()
	if selected.Addr().String() == occupied.Addr().String() {
		t.Fatal("selected the occupied port")
	}
	addr := selected.Addr().(*net.TCPAddr)
	if !addr.IP.IsLoopback() || addr.Port == 0 {
		t.Fatalf("invalid selected address: %s", addr)
	}
	cfg.Bonsai.Endpoint = "http://" + selected.Addr().String()
	data, err := servicePlist(cfg, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var service struct{ ProgramArguments []string }
	if _, err := plist.Unmarshal(data, &service); err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(selected.Addr().String())
	if argumentValue(service.ProgramArguments, "--port") != port {
		t.Fatalf("service did not use selected port: %v", service.ProgramArguments)
	}
	if err := selected.Close(); err != nil {
		t.Fatal(err)
	}
	reused, err := reservePort(cfg.Bonsai)
	if err != nil {
		t.Fatal(err)
	}
	defer reused.Close()
	if reused.Addr().String() != selected.Addr().String() {
		t.Fatal("free configured port was not reused")
	}
}

// Opt in with the directory containing the installed pinned binaries and model.
// Runs only a child process; it never changes the user's launchd service.
func TestManagedServiceLiveStartupWithIPv6PortOccupied(t *testing.T) {
	dir := os.Getenv("PAPERLESS_BONSAI_TEST_RUNTIME")
	if runtime.GOOS != "darwin" || dir == "" {
		t.Skip("requires macOS and PAPERLESS_BONSAI_TEST_RUNTIME")
	}
	ipv4, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ipv4.Close()
	_, port, _ := net.SplitHostPort(ipv4.Addr().String())
	ipv6, err := net.Listen("tcp6", "[::1]:"+port)
	if err != nil {
		t.Fatal(err)
	}
	other := httptest.NewUnstartedServer(http.NotFoundHandler())
	other.Listener.Close()
	other.Listener = ipv6
	other.Start()
	defer other.Close()

	cfg := config.Default()
	cfg.Bonsai.Endpoint = "http://" + ipv4.Addr().String()
	cfg.LLM.ContextTokens = 4096
	data, err := servicePlist(cfg, dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var service struct {
		ProgramArguments     []string
		WorkingDirectory     string
		EnvironmentVariables map[string]string
	}
	if _, err := plist.Unmarshal(data, &service); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, service.ProgramArguments[0], service.ProgramArguments[1:]...)
	cmd.Dir = service.WorkingDirectory
	for k, v := range service.EnvironmentVariables {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	configureInstallCommand(cmd)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := ipv4.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
		if t.Failed() {
			t.Log(output.String())
		}
	}()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := Available(ctx, cfg.Bonsai)
		if err == nil {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("managed server failed to become ready with IPv6 occupied: %v", err)
		case <-ticker.C:
		}
	}
}

func TestManagedPortRejectsCustomOrInvalidEndpoints(t *testing.T) {
	for _, endpoint := range []string{"http://0.0.0.0:8080", "https://127.0.0.1:8080", "http://127.0.0.1", "http://127.0.0.1:0", "http://127.0.0.1:65536", "http://user@127.0.0.1:8080", "http://127.0.0.1:8080/custom"} {
		t.Run(endpoint, func(t *testing.T) {
			cfg := config.Default().Bonsai
			cfg.Endpoint = endpoint
			if _, err := reservePort(cfg); err == nil {
				t.Fatal("accepted unsupported managed endpoint")
			}
		})
	}
}
