package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"paperless/internal/config"
	"paperless/internal/db"
)

const databaseBackupRetention = 14

type databaseBackupStatus struct {
	Directory string `json:"directory"`
	Latest    string `json:"latest"`
	Count     int    `json:"count"`
	Available bool   `json:"available"`
}

func BackupDatabase(ctx context.Context, cfg config.Config) (string, error) {
	processor, cleanup, err := newProcessor(ctx, cfg)
	if err != nil {
		return "", err
	}
	defer cleanup()
	return processor.backupDatabase(ctx)
}

func (p *Processor) backupDatabase(ctx context.Context) (string, error) {
	root, err := validateDocumentsDirectory(p.cfg.Paths.ArchiveRoot)
	if err != nil {
		return "", err
	}
	backupDir := filepath.Join(root, ".paperless-backups")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return "", err
	}

	local, err := os.CreateTemp(p.cfg.Paths.StateDir, ".paperless-snapshot-*.sqlite")
	if err != nil {
		return "", err
	}
	localPath := local.Name()
	if err := local.Close(); err != nil {
		return "", err
	}
	if err := os.Remove(localPath); err != nil {
		return "", err
	}
	defer os.Remove(localPath)
	if err := p.store.Snapshot(ctx, localPath); err != nil {
		return "", err
	}
	if err := db.IntegrityCheck(ctx, localPath); err != nil {
		return "", err
	}

	staging, err := os.CreateTemp(backupDir, ".paperless-backup-*.tmp")
	if err != nil {
		return "", err
	}
	stagingPath := staging.Name()
	defer os.Remove(stagingPath)
	if err := staging.Chmod(0o600); err != nil {
		staging.Close()
		return "", err
	}
	source, err := os.Open(localPath)
	if err != nil {
		staging.Close()
		return "", err
	}
	_, copyErr := io.Copy(staging, source)
	sourceCloseErr := source.Close()
	syncErr := staging.Sync()
	stagingCloseErr := staging.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if sourceCloseErr != nil {
		return "", sourceCloseErr
	}
	if syncErr != nil {
		return "", syncErr
	}
	if stagingCloseErr != nil {
		return "", stagingCloseErr
	}

	filename := "paperless-" + time.Now().UTC().Format("20060102T150405.000000000Z") + ".sqlite"
	destination := filepath.Join(backupDir, filename)
	if err := os.Rename(stagingPath, destination); err != nil {
		return "", err
	}
	if err := pruneDatabaseBackups(backupDir, databaseBackupRetention); err != nil {
		return destination, err
	}
	p.notifyDashboard()
	return destination, nil
}

func (p *Processor) handleDatabaseBackupAPI(w http.ResponseWriter, r *http.Request) {
	if !localRequest(r) {
		writeAPIError(w, errors.New("backups can be started only from this Mac"), http.StatusForbidden)
		return
	}
	path, err := p.backupDatabase(r.Context())
	if err != nil {
		writeAPIError(w, err, http.StatusServiceUnavailable)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"path": path})
}

func databaseBackups(root string) databaseBackupStatus {
	status := databaseBackupStatus{Directory: filepath.Join(root, ".paperless-backups")}
	if strings.TrimSpace(root) == "" {
		status.Directory = ""
		return status
	}
	entries, err := os.ReadDir(status.Directory)
	if err != nil {
		return status
	}
	for _, entry := range entries {
		if !entry.IsDir() && isDatabaseBackup(entry.Name()) {
			status.Count++
			if entry.Name() > status.Latest {
				status.Latest = entry.Name()
			}
		}
	}
	status.Available = true
	return status
}

func pruneDatabaseBackups(directory string, retain int) error {
	if retain < 1 {
		return errors.New("backup retention must be at least one")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && isDatabaseBackup(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names[:max(0, len(names)-retain)] {
		if err := os.Remove(filepath.Join(directory, name)); err != nil {
			return fmt.Errorf("remove expired database backup %s: %w", name, err)
		}
	}
	return nil
}

func isDatabaseBackup(name string) bool {
	return strings.HasPrefix(name, "paperless-") && strings.HasSuffix(name, ".sqlite")
}
