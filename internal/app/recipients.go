package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"paperless/internal/classify"
	"paperless/internal/config"
	"paperless/internal/progress"
)

func (p *Processor) classifyDocument(ctx context.Context, text, filename string, date time.Time, folders []string, reporter progress.Reporter, layout ...string) classify.Classification {
	cfg := p.cfg
	profiles, err := p.store.RecipientProfiles(ctx)
	if err != nil {
		reporter.Warn("classify", "recipients", "Could not load recipient profiles; requiring review.", 0, 0, 87)
	}
	cfg.RecipientProfiles = append(append([]config.RecipientProfile{}, cfg.RecipientProfiles...), profiles...)
	addresses, addressErr := p.store.RecipientAddresses(ctx)
	if addressErr != nil {
		reporter.Warn("classify", "addresses", "Could not load recipient addresses; requiring review.", 0, 0, 87)
	}
	cfg.RecipientAddresses = append(append([]string{}, cfg.RecipientAddresses...), addresses...)
	rows, historyErr := p.store.Queries.ListRoutingExamples(ctx)
	if historyErr != nil {
		reporter.Warn("classify", "learning", "Could not load approved filing examples; requiring review.", 0, 0, 87)
	}
	history := []classify.RoutingExample{}
	for _, row := range rows {
		history = append(history, classify.RoutingExample{Sender: row.Sender, Recipient: row.Recipient, RecipientScope: row.RecipientScope, Folder: row.Folder, Filename: fmt.Sprint(row.Filename), Approvals: row.Approvals})
	}
	folders = classify.RecipientFolders(cfg, text, folders)
	candidates := classify.LearnedFolders(cfg, text, history, folders)
	for _, folder := range candidateFolders(text, folders, 24) {
		if !containsFolder(candidates, folder) {
			candidates = append(candidates, folder)
		}
	}
	reporter.Info("classify", "learning", fmt.Sprintf("Loaded %d saved recipient profiles and %d approved routing patterns.", len(profiles), len(history)), 0, 0, 87)
	c := classify.ClassifyWithHistory(ctx, cfg, text, filename, date, candidates, history, reporter, layout...)
	if err != nil || historyErr != nil || addressErr != nil {
		c.RecipientNeedsReview = true
	}
	return c
}

func (p *Processor) handleSaveRecipientAddressesAPI(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Addresses *[]string `json:"addresses"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16_384)).Decode(&input); err != nil {
		writeAPIError(w, err, http.StatusBadRequest)
		return
	}
	if input.Addresses == nil {
		writeAPIError(w, fmt.Errorf("addresses must be an array; use an empty array to clear saved addresses"), http.StatusBadRequest)
		return
	}
	if err := p.store.SaveRecipientAddresses(r.Context(), *input.Addresses); err != nil {
		writeAPIError(w, err, http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func containsFolder(folders []string, want string) bool {
	for _, folder := range folders {
		if folder == want {
			return true
		}
	}
	return false
}

func (p *Processor) handleSaveRecipientAPI(w http.ResponseWriter, r *http.Request) {
	var profile config.RecipientProfile
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16_384)).Decode(&profile); err != nil {
		writeAPIError(w, err, http.StatusBadRequest)
		return
	}
	profile.FolderPrefix = strings.TrimSpace(profile.FolderPrefix)
	if profile.FolderPrefix != "" {
		if filepath.IsAbs(profile.FolderPrefix) || strings.Contains(profile.FolderPrefix, "\\") {
			writeAPIError(w, fmt.Errorf("filing area must be a relative folder inside the archive"), http.StatusBadRequest)
			return
		}
		for _, part := range strings.Split(profile.FolderPrefix, "/") {
			if part == ".." || part == "." || part == "" {
				writeAPIError(w, fmt.Errorf("filing area must use folder names without traversal"), http.StatusBadRequest)
				return
			}
		}
	}
	if err := p.store.SaveRecipientProfile(r.Context(), profile); err != nil {
		writeAPIError(w, err, http.StatusBadRequest)
		return
	}
	p.notifyDashboard()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
