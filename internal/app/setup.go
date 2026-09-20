package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"paperless/internal/bonsai"
	"paperless/internal/config"
	"paperless/internal/fm"
)

func chooseDocumentsDirectory(ctx context.Context) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", errors.New("the graphical folder chooser is available on macOS; use paperless configure --archive /path/to/documents")
	}
	script := `POSIX path of (choose folder with prompt "Choose the base documents directory for Paperless. It may be a local folder or a locally synced cloud folder.")`
	output, err := exec.CommandContext(ctx, "/usr/bin/osascript", "-e", script).Output()
	if err != nil {
		return "", errors.New("folder selection was cancelled")
	}
	return strings.TrimSpace(string(output)), nil
}

// SelectDocumentsDirectory opens the native macOS picker and verifies that the
// selected local or cloud-synced folder is writable by Paperless.
func SelectDocumentsDirectory(ctx context.Context) (string, error) {
	selected, err := chooseDocumentsDirectory(ctx)
	if err != nil {
		return "", err
	}
	return validateDocumentsDirectory(selected)
}

func validateDocumentsDirectory(selected string) (string, error) {
	selected = strings.TrimSpace(selected)
	if selected == "" {
		return "", errors.New("choose a documents directory")
	}
	if strings.Contains(selected, "://") {
		return "", errors.New("choose a locally available folder, not a web address")
	}
	abs, err := filepath.Abs(selected)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("documents directory is unavailable: %w", err)
	}
	if !info.IsDir() {
		return "", errors.New("the selected documents location is not a directory")
	}
	probe, err := os.CreateTemp(abs, ".paperless-access-check-*")
	if err != nil {
		return "", fmt.Errorf("Paperless cannot write to the selected directory: %w", err)
	}
	probePath := probe.Name()
	if closeErr := probe.Close(); closeErr != nil {
		_ = os.Remove(probePath)
		return "", closeErr
	}
	if err := os.Remove(probePath); err != nil {
		return "", fmt.Errorf("Paperless cannot clean up files in the selected directory: %w", err)
	}
	return abs, nil
}

func archiveDirectoryStatus(root string) (bool, string) {
	if strings.TrimSpace(root) == "" {
		return false, "Choose a base documents directory to finish setup."
	}
	info, err := os.Stat(root)
	if err != nil {
		return false, err.Error()
	}
	if !info.IsDir() {
		return false, "The configured documents location is not a directory."
	}
	directory, err := os.Open(root)
	if err != nil {
		return false, err.Error()
	}
	_, readErr := directory.Readdirnames(1)
	closeErr := directory.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return false, readErr.Error()
	}
	if closeErr != nil {
		return false, closeErr.Error()
	}
	return true, ""
}

func (p *Processor) handleChooseDocumentsDirectoryAPI(w http.ResponseWriter, r *http.Request) {
	if !localRequest(r) {
		writeAPIError(w, errors.New("setup changes are accepted only from this Mac"), http.StatusForbidden)
		return
	}
	selected, err := p.directoryChooser(r.Context())
	if err != nil {
		writeAPIError(w, err, http.StatusBadRequest)
		return
	}
	selected, err = validateDocumentsDirectory(selected)
	if err != nil {
		writeAPIError(w, err, http.StatusBadRequest)
		return
	}
	cfg, err := config.Load(p.configPath)
	if err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	if cfg.NeedsSetup() {
		cfg.Setup.Step = "scanner"
	}
	cfg.Paths.ArchiveRoot = selected
	if _, err := config.Write(p.configPath, cfg); err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"documents_directory": selected,
		"restarting":          true,
	})
	p.requestRestart()
}

func (p *Processor) requestRestart() {
	go func() {
		timer := time.NewTimer(300 * time.Millisecond)
		defer timer.Stop()
		<-timer.C
		select {
		case p.restart <- struct{}{}:
		default:
		}
	}()
}

func (p *Processor) handleModelSetupAPI(w http.ResponseWriter, r *http.Request) {
	if !localRequest(r) {
		writeAPIError(w, errors.New("setup changes are accepted only from this Mac"), http.StatusForbidden)
		return
	}
	var input struct {
		Provider string `json:"provider"`
		Enabled  *bool  `json:"enabled"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeAPIError(w, err, http.StatusBadRequest)
		return
	}
	if p.modelInstallBusy.Load() {
		writeAPIError(w, errors.New("wait for Bonsai installation to finish before changing models"), http.StatusConflict)
		return
	}
	if input.Provider != "ollama" && input.Provider != "fm" && input.Provider != "bonsai" {
		writeAPIError(w, errors.New("provider must be ollama, fm, or bonsai"), http.StatusBadRequest)
		return
	}
	enabled := input.Enabled == nil || *input.Enabled
	if enabled && input.Provider == "fm" {
		if err := fm.Available(r.Context()); err != nil {
			writeAPIError(w, err, http.StatusBadRequest)
			return
		}
	}
	// Load the latest saved settings so switching models preserves changes made
	// through other setup controls while this process is still running.
	cfg, err := config.Load(p.configPath)
	if err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	if cfg.NeedsSetup() && cfg.SetupStep() != "model" && cfg.SetupStep() != "ready" {
		writeAPIError(w, errors.New("choose a documents folder and continue through the scanner step first"), http.StatusConflict)
		return
	}
	cfg.LLM.Provider = input.Provider
	cfg.LLM.Enabled = enabled
	if enabled && input.Provider == "bonsai" {
		if err := bonsai.Available(r.Context(), cfg.Bonsai); err != nil {
			writeAPIError(w, err, http.StatusBadRequest)
			return
		}
	}
	if cfg.NeedsSetup() {
		if err := setupModelAvailable(r.Context(), cfg); err != nil {
			writeAPIError(w, err, http.StatusBadRequest)
			return
		}
		cfg.Setup.Step = "ready"
	}
	if _, err := config.Write(p.configPath, cfg); err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"provider": input.Provider, "restarting": true})
	p.requestRestart()
}

func setupModelAvailable(ctx context.Context, cfg config.Config) error {
	if !cfg.LLM.Enabled {
		return nil
	}
	switch cfg.LLM.Provider {
	case "fm":
		return fm.Available(ctx)
	case "bonsai":
		return bonsai.Available(ctx, cfg.Bonsai)
	case "ollama":
		models, err := ollamaModelList(ctx, cfg)
		if err != nil {
			return fmt.Errorf("start Ollama before continuing, or choose local rules for now: %w", err)
		}
		ok, detail := ollamaModelDetail(cfg.LLM.Model, models)
		if !ok {
			return errors.New(detail)
		}
		return nil
	default:
		return errors.New("choose a supported local model")
	}
}

func (p *Processor) handleSetupProgressAPI(w http.ResponseWriter, r *http.Request) {
	if !localRequest(r) {
		writeAPIError(w, errors.New("setup changes are accepted only from this Mac"), http.StatusForbidden)
		return
	}
	var input struct {
		Step string `json:"step"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeAPIError(w, err, http.StatusBadRequest)
		return
	}
	if p.modelInstallBusy.Load() {
		writeAPIError(w, errors.New("wait for model installation to finish"), http.StatusConflict)
		return
	}
	cfg, err := config.Load(p.configPath)
	if err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	current := cfg.SetupStep()
	switch input.Step {
	case "model":
		if current != "scanner" && current != "model" && current != "ready" {
			writeAPIError(w, errors.New("choose a documents folder first"), http.StatusConflict)
			return
		}
	case "complete":
		if current != "ready" {
			writeAPIError(w, errors.New("finish the model step before completing setup"), http.StatusConflict)
			return
		}
		if _, err := validateDocumentsDirectory(cfg.Paths.ArchiveRoot); err != nil {
			writeAPIError(w, err, http.StatusBadRequest)
			return
		}
		if err := setupModelAvailable(r.Context(), cfg); err != nil {
			writeAPIError(w, err, http.StatusBadRequest)
			return
		}
	default:
		writeAPIError(w, errors.New("setup step must be model or complete"), http.StatusBadRequest)
		return
	}
	cfg.Setup.Step = input.Step
	if _, err := config.Write(p.configPath, cfg); err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"step": input.Step, "restarting": true})
	p.requestRestart()
}

// Installation streams stage updates while the large model downloads. The
// provider is saved separately, only after installation and readiness succeed.
func (p *Processor) handleBonsaiInstallAPI(w http.ResponseWriter, r *http.Request) {
	if !localRequest(r) {
		writeAPIError(w, errors.New("setup changes are accepted only from this Mac"), http.StatusForbidden)
		return
	}
	// Require JSON so a cross-origin HTML form cannot trigger software installs.
	if r.Header.Get("Content-Type") != "application/json" {
		writeAPIError(w, errors.New("installation requires application/json"), http.StatusUnsupportedMediaType)
		return
	}
	var input struct {
		Install bool `json:"install"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || !input.Install {
		writeAPIError(w, errors.New("installation requires install: true"), http.StatusBadRequest)
		return
	}
	if !p.modelInstallBusy.CompareAndSwap(false, true) {
		writeAPIError(w, errors.New("Bonsai installation is already running"), http.StatusConflict)
		return
	}
	defer p.modelInstallBusy.Store(false)
	cfg, err := config.Load(p.configPath)
	if err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	if err := os.MkdirAll(cfg.Paths.StateDir, 0o700); err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	logPath := filepath.Join(cfg.Paths.StateDir, "bonsai-install.log")
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	defer log.Close()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	encoder := json.NewEncoder(w)
	send := func(value map[string]any) {
		_ = encoder.Encode(value)
		_ = http.NewResponseController(w).Flush()
	}
	install := p.modelInstaller
	if install == nil {
		install = bonsai.Install
	}
	err = installBonsai(r.Context(), cfg, p.configPath, log, log, func(message string) {
		send(map[string]any{"message": message})
	}, install)
	if err != nil {
		send(map[string]any{"error": err.Error() + "; installation log: " + logPath})
		return
	}
	send(map[string]any{"done": true})
}

func (p *Processor) handleOpenSharingSettingsAPI(w http.ResponseWriter, r *http.Request) {
	if !localRequest(r) {
		writeAPIError(w, errors.New("setup changes are accepted only from this Mac"), http.StatusForbidden)
		return
	}
	if err := p.sharingSettingsOpener(r.Context()); err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func openSharingSettings(ctx context.Context) error {
	if runtime.GOOS != "darwin" {
		return errors.New("sharing settings are available on macOS only")
	}
	return exec.CommandContext(ctx, "/usr/bin/open", "x-apple.systempreferences:com.apple.Sharing-Settings.extension").Run()
}

func localRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	return net.ParseIP(host).IsLoopback()
}
