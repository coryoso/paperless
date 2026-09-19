package app

import (
	"context"
	"errors"
	"fmt"
	"io"
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

func TestBonsaiSetupChecksServerAndPreservesOllamaConfig(t *testing.T) {
	ready := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/health" {
			if !ready {
				w.WriteHeader(503)
				return
			}
			fmt.Fprint(w, `{"status":"ok"}`)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"Bonsai-8B"}]}`)
	}))
	defer server.Close()
	cfg := config.Default()
	cfg.Bonsai.Endpoint = server.URL
	cfg.LLM.Model = "my-ollama"
	cfg.Paths.ArchiveRoot = "/example/documents"
	path := filepath.Join(t.TempDir(), "config.toml")
	if _, err := config.Write(path, cfg); err != nil {
		t.Fatal(err)
	}
	p := &Processor{configPath: path, restart: make(chan struct{}, 1)}
	for _, provider := range []string{"bonsai", "ollama"} {
		r := httptest.NewRequest(http.MethodPost, "/api/setup/model", strings.NewReader(`{"provider":"`+provider+`"}`))
		r.RemoteAddr = "127.0.0.1:1234"
		w := httptest.NewRecorder()
		p.handleModelSetupAPI(w, r)
		if w.Code != 202 {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		saved, err := config.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if saved.LLM.Provider != provider || saved.LLM.Model != "my-ollama" || saved.Bonsai.Endpoint != server.URL || saved.Paths.ArchiveRoot != cfg.Paths.ArchiveRoot {
			t.Fatalf("config=%+v", saved)
		}
		select {
		case <-p.restart:
		case <-time.After(time.Second):
			t.Fatal("no restart")
		}
	}
	ready = false
	r := httptest.NewRequest(http.MethodPost, "/api/setup/model", strings.NewReader(`{"provider":"bonsai"}`))
	r.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	p.handleModelSetupAPI(w, r)
	if w.Code != 400 {
		t.Fatalf("saved loading model: %d", w.Code)
	}
	saved, _ := config.Load(path)
	if saved.LLM.Provider != "ollama" {
		t.Fatal("failed switch changed saved provider")
	}
}

func TestBonsaiInstallationStreamsProgressAndLeavesConfigUntilSaved(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			cfg := config.Default()
			cfg.Paths.StateDir = t.TempDir()
			path := filepath.Join(t.TempDir(), "config.toml")
			if _, err := config.Write(path, cfg); err != nil {
				t.Fatal(err)
			}
			p := &Processor{configPath: path, restart: make(chan struct{}, 1), modelInstaller: func(ctx context.Context, cfg config.Config, out, errout io.Writer, report func(string)) error {
				report("Downloading model…")
				if fail {
					return errors.New("download failed")
				}
				return nil
			}}
			r := httptest.NewRequest(http.MethodPost, "/api/setup/bonsai/install", strings.NewReader(`{"install":true}`))
			r.RemoteAddr = "127.0.0.1:1234"
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			p.handleBonsaiInstallAPI(w, r)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "Downloading model") {
				t.Fatalf("response=%s", w.Body.String())
			}
			if strings.Contains(w.Body.String(), `"done":true`) == fail || strings.Contains(w.Body.String(), `"error"`) != fail {
				t.Fatalf("bad completion=%s", w.Body.String())
			}
			saved, _ := config.Load(path)
			if saved.LLM.Provider != "ollama" {
				t.Fatal("installation changed model before save")
			}
			if p.modelInstallBusy.Load() {
				t.Fatal("busy flag leaked")
			}
			if len(p.restart) != 0 {
				t.Fatal("installation restarted app before save")
			}
		})
	}
}

func TestBonsaiInstallRejectsRemoteInvalidAndConcurrentRequests(t *testing.T) {
	for _, tt := range []struct {
		name, remote, body, contentType string
		busy                            bool
		status                          int
	}{
		{"remote", "192.168.1.4:1234", `{"install":true}`, "application/json", false, 403},
		{"form", "127.0.0.1:1234", `{"install":true}`, "text/plain", false, 415},
		{"invalid", "127.0.0.1:1234", `{}`, "application/json", false, 400},
		{"busy", "127.0.0.1:1234", `{"install":true}`, "application/json", true, 409},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := &Processor{modelInstaller: func(context.Context, config.Config, io.Writer, io.Writer, func(string)) error {
				t.Fatal("unexpected install")
				return nil
			}}
			p.modelInstallBusy.Store(tt.busy)
			r := httptest.NewRequest("POST", "/api/setup/bonsai/install", strings.NewReader(tt.body))
			r.RemoteAddr = tt.remote
			r.Header.Set("Content-Type", tt.contentType)
			w := httptest.NewRecorder()
			p.handleBonsaiInstallAPI(w, r)
			if w.Code != tt.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}
