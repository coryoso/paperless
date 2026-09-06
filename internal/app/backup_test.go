package app

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestDatabaseBackupCreatesRestorableSnapshotInDocumentsRoot(t *testing.T) {
	cfg := testServerConfig(t.TempDir())
	p, cleanup, err := newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err := p.store.Conn().ExecContext(t.Context(), "INSERT INTO jobs (id, source_filename, scan_timestamp, updated_at, status) VALUES ('backup-job', 'scan.pdf', 'now', 'now', 'archived')"); err != nil {
		t.Fatal(err)
	}
	backup, err := p.backupDatabase(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(backup) != filepath.Join(cfg.Paths.ArchiveRoot, ".paperless-backups") {
		t.Fatalf("backup path = %q", backup)
	}
	copyDB, err := sql.Open("sqlite", backup)
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	var count int
	if err := copyDB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM jobs WHERE id = 'backup-job'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("backed-up job count = %d", count)
	}
}

func TestPruneDatabaseBackupsKeepsNewestSnapshots(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"paperless-1.sqlite", "paperless-2.sqlite", "paperless-3.sqlite", "unrelated.txt"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := pruneDatabaseBackups(directory, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(directory, "paperless-1.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("oldest backup still exists: %v", err)
	}
	for _, name := range []string{"paperless-2.sqlite", "paperless-3.sqlite", "unrelated.txt"} {
		if _, err := os.Stat(filepath.Join(directory, name)); err != nil {
			t.Fatalf("expected %s to remain: %v", name, err)
		}
	}
}
