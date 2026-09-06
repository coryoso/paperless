package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSnapshotCreatesConsistentDatabaseCopy(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "source.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Conn().ExecContext(ctx, "INSERT INTO jobs (id, source_filename, scan_timestamp, updated_at, status) VALUES ('job', 'scan.pdf', 'now', 'now', 'archived')"); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "snapshot.sqlite")
	if err := store.Snapshot(ctx, destination); err != nil {
		t.Fatal(err)
	}
	if err := IntegrityCheck(ctx, destination); err != nil {
		t.Fatal(err)
	}
}
