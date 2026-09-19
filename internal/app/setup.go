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
	cfg := p.cfg
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
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeAPIError(w, err, http.StatusBadRequest)
		return
	}
	if input.Provider != "ollama" && input.Provider != "fm" {
		writeAPIError(w, errors.New("provider must be ollama or fm"), http.StatusBadRequest)
		return
	}
	if input.Provider == "fm" {
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
	cfg.LLM.Provider = input.Provider
	cfg.LLM.Enabled = true
	if _, err := config.Write(p.configPath, cfg); err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"provider": input.Provider, "restarting": true})
	p.requestRestart()
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
