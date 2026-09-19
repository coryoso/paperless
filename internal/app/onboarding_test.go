package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"paperless/internal/config"
)

func TestSetupGuideResumesAndOnlyCompletesAfterModelChoice(t *testing.T) {
	cfg := testServerConfig(t.TempDir())
	cfg.Paths.ArchiveRoot = ""
	cfg.LLM.Enabled = true
	path := filepath.Join(t.TempDir(), "config.toml")
	if _, err := config.Write(path, cfg); err != nil {
		t.Fatal(err)
	}
	documents := t.TempDir()
	p := &Processor{cfg: cfg, configPath: path, restart: make(chan struct{}, 1), directoryChooser: func(context.Context) (string, error) { return documents, nil }}
	post := func(handler http.HandlerFunc, body string, status int) {
		t.Helper()
		r := httptest.NewRequest("POST", "/api/setup/test", strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		w := httptest.NewRecorder()
		handler(w, r)
		if w.Code != status {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		if status == 202 {
			select {
			case <-p.restart:
			case <-time.After(time.Second):
				t.Fatal("no restart")
			}
		}
	}
	post(p.handleSetupProgressAPI, `{"step":"complete"}`, 409)
	post(p.handleChooseDocumentsDirectoryAPI, "", 202)
	saved, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.SetupStep() != "scanner" || !saved.NeedsSetup() {
		t.Fatalf("folder prematurely finished guide: %+v", saved.Setup)
	}
	post(p.handleSetupProgressAPI, `{"step":"complete"}`, 409)
	post(p.handleSetupProgressAPI, `{"step":"model"}`, 202)
	post(p.handleModelSetupAPI, `{"provider":"ollama","enabled":false}`, 202)
	saved, err = config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.SetupStep() != "ready" || !saved.NeedsSetup() || saved.LLM.Enabled {
		t.Fatalf("model choice=%+v %+v", saved.Setup, saved.LLM)
	}
	post(p.handleSetupProgressAPI, `{"step":"complete"}`, 202)
	saved, err = config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.NeedsSetup() || saved.Paths.ArchiveRoot != documents {
		t.Fatalf("setup not complete: %+v", saved)
	}
}

func TestUnfinishedGuidePausesInboxAndUploads(t *testing.T) {
	cfg := testServerConfig(t.TempDir())
	cfg.Setup.Step = "model"
	p := &Processor{cfg: cfg}
	if count, err := p.ProcessInboxOnce(t.Context()); count != 0 || err != nil {
		t.Fatalf("processed before setup: %d %v", count, err)
	}
	r := httptest.NewRequest("POST", "/api/uploads", strings.NewReader("document"))
	w := httptest.NewRecorder()
	p.handleUploadAPI(w, r)
	if w.Code != http.StatusConflict {
		t.Fatalf("upload status=%d", w.Code)
	}
}

func TestFinishSetupRechecksFolderAndModel(t *testing.T) {
	cfg := testServerConfig(t.TempDir())
	cfg.Setup.Step = "ready"
	cfg.Paths.ArchiveRoot = t.TempDir()
	cfg.LLM.Enabled = true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }))
	defer server.Close()
	cfg.LLM.Endpoint = server.URL
	path := filepath.Join(t.TempDir(), "config.toml")
	if _, err := config.Write(path, cfg); err != nil {
		t.Fatal(err)
	}
	p := &Processor{configPath: path}
	finish := func() {
		t.Helper()
		r := httptest.NewRequest("POST", "/api/setup/progress", strings.NewReader(`{"step":"complete"}`))
		r.RemoteAddr = "127.0.0.1:1234"
		w := httptest.NewRecorder()
		p.handleSetupProgressAPI(w, r)
		if w.Code != 400 {
			t.Fatalf("accepted incomplete setup: %d %s", w.Code, w.Body.String())
		}
		saved, _ := config.Load(path)
		if saved.SetupStep() != "ready" {
			t.Fatal("failure changed progress")
		}
	}
	finish()
	cfg.LLM.Enabled = false
	if err := os.Remove(cfg.Paths.ArchiveRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Write(path, cfg); err != nil {
		t.Fatal(err)
	}
	finish()
}

func TestSetupProgressRejectsRemoteAndUnknownSteps(t *testing.T) {
	for _, tt := range []struct {
		remote, body string
		status       int
	}{
		{"10.0.0.5:1234", `{"step":"complete"}`, 403},
		{"127.0.0.1:1234", `{"step":"bad"}`, 400},
		{"127.0.0.1:1234", `not json`, 400},
	} {
		p := &Processor{configPath: filepath.Join(t.TempDir(), "config.toml")}
		r := httptest.NewRequest("POST", "/api/setup/progress", strings.NewReader(tt.body))
		r.RemoteAddr = tt.remote
		w := httptest.NewRecorder()
		p.handleSetupProgressAPI(w, r)
		if w.Code != tt.status {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
}

func TestReloadOnSetupChangesResumesWithSavedConfig(t *testing.T) {
	cfg := config.Default()
	path := filepath.Join(t.TempDir(), "config.toml")
	calls := 0
	err := reloadOnSetupChanges(t.Context(), cfg, path, func(ctx context.Context, got config.Config, gotPath string) error {
		calls++
		if gotPath != path {
			t.Fatal("lost config path")
		}
		if calls == 1 {
			got.Paths.ArchiveRoot = t.TempDir()
			got.Setup.Step = "scanner"
			if _, err := config.Write(path, got); err != nil {
				t.Fatal(err)
			}
			return errRestartRequested
		}
		if calls != 2 || got.SetupStep() != "scanner" {
			t.Fatalf("did not reload saved setup: %+v", got.Setup)
		}
		return nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("reload=%v calls=%d", err, calls)
	}
	want := errors.New("startup failed")
	if err := reloadOnSetupChanges(t.Context(), cfg, path, func(context.Context, config.Config, string) error { return want }); !errors.Is(err, want) {
		t.Fatal("startup failure swallowed")
	}
}
