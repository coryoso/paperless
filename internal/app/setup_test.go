package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"paperless/internal/config"
)

func TestValidateDocumentsDirectoryAcceptsWritableLocalFolder(t *testing.T) {
	want := t.TempDir()
	got, err := validateDocumentsDirectory(want)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("directory = %q, want %q", got, want)
	}
}

func TestValidateDocumentsDirectoryRejectsURL(t *testing.T) {
	if _, err := validateDocumentsDirectory("https://drive.example/documents"); err == nil {
		t.Fatal("expected a web address to be rejected")
	}
}

func TestChooseDocumentsDirectoryPersistsConfigAndRequestsRestart(t *testing.T) {
	documents := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.toml")
	p := &Processor{
		cfg:        config.Default(),
		configPath: configPath,
		restart:    make(chan struct{}, 1),
		directoryChooser: func(context.Context) (string, error) {
			return documents, nil
		},
	}
	request := httptest.NewRequest(http.MethodPost, "/api/setup/documents-directory", nil)
	request.RemoteAddr = "127.0.0.1:54321"
	recorder := httptest.NewRecorder()
	p.handleChooseDocumentsDirectoryAPI(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Paths.ArchiveRoot != documents {
		t.Fatalf("archive root = %q, want %q", cfg.Paths.ArchiveRoot, documents)
	}
	if info, err := os.Stat(configPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config permissions: info=%v err=%v", info, err)
	}
	select {
	case <-p.restart:
	case <-time.After(time.Second):
		t.Fatal("setup did not request a service restart")
	}
}

func TestSetupChangesRejectNonLocalRequests(t *testing.T) {
	p := &Processor{directoryChooser: func(context.Context) (string, error) { return t.TempDir(), nil }}
	request := httptest.NewRequest(http.MethodPost, "/api/setup/documents-directory", nil)
	recorder := httptest.NewRecorder()
	p.handleChooseDocumentsDirectoryAPI(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}
