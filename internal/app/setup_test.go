package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func TestModelSetupPersistsProviderAndRestarts(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fm"), []byte("#!/bin/sh\necho 'System model available'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	path := filepath.Join(dir, "config.toml")
	cfg := config.Default()
	cfg.Paths.ArchiveRoot = filepath.Join(dir, "Documents")
	cfg.Service.Port = 9988
	if _, err := config.Write(path, cfg); err != nil {
		t.Fatal(err)
	}
	p := &Processor{cfg: config.Default(), configPath: path, restart: make(chan struct{}, 1)}
	for _, provider := range []string{"fm", "ollama"} {
		request := httptest.NewRequest(http.MethodPost, "/api/setup/model", strings.NewReader(`{"provider":"`+provider+`"}`))
		request.RemoteAddr = "127.0.0.1:54321"
		recorder := httptest.NewRecorder()
		p.handleModelSetupAPI(recorder, request)
		if recorder.Code != http.StatusAccepted {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		saved, err := config.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if saved.LLM.Provider != provider || !saved.LLM.Enabled || saved.Paths.ArchiveRoot != cfg.Paths.ArchiveRoot || saved.Service.Port != 9988 {
			t.Fatalf("saved config = %+v", saved)
		}
		select {
		case <-p.restart:
		case <-time.After(time.Second):
			t.Fatal("model setup did not request restart")
		}
	}
}

func TestModelSetupRejectsInvalidOrUnavailableProviderWithoutSaving(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.toml")
	p := &Processor{configPath: path, restart: make(chan struct{}, 1)}
	for _, tt := range []struct {
		body, remote string
		status       int
	}{
		{`{"provider":"fm"}`, "127.0.0.1:54321", http.StatusBadRequest},
		{`{"provider":"unknown"}`, "127.0.0.1:54321", http.StatusBadRequest},
		{`invalid`, "127.0.0.1:54321", http.StatusBadRequest},
		{`{"provider":"ollama"}`, "192.168.1.2:54321", http.StatusForbidden},
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/setup/model", strings.NewReader(tt.body))
		request.RemoteAddr = tt.remote
		recorder := httptest.NewRecorder()
		p.handleModelSetupAPI(recorder, request)
		if recorder.Code != tt.status {
			t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("invalid setup wrote configuration: %v", err)
		}
	}
}
