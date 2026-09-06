package db

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"paperless/internal/config"
	"paperless/internal/db/sqlc"
)

func TestOpenProtectsDatabaseFromOtherUsers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "paperless.sqlite")
	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Fatalf("database permissions = %o, want %o", got, want)
	}
}

func TestMigrateAndTypedQueries(t *testing.T) {
	store, err := Open(t.Context(), filepath.Join(t.TempDir(), "paperless.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := Now()
	if err := store.Queries.CreateJob(t.Context(), sqlc.CreateJobParams{
		ID:             "job-1",
		SourceFilename: "scan.pdf",
		CurrentPath:    "/tmp/scan.pdf",
		ScanTimestamp:  now,
		UpdatedAt:      now,
		Status:         "received",
	}); err != nil {
		t.Fatal(err)
	}
	job, err := store.Queries.GetJob(t.Context(), "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if job.SourceFilename != "scan.pdf" {
		t.Fatalf("source filename = %q", job.SourceFilename)
	}
}

func TestLearnApproval(t *testing.T) {
	store, err := Open(t.Context(), filepath.Join(t.TempDir(), "paperless.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.LearnApproval(t.Context(), Approval{
		JobID:          "",
		Sender:         "rewe",
		Recipient:      "alex-example",
		RecipientScope: "personal",
		DocumentType:   "receipt",
		Folder:         "Receipts/Groceries",
		Filename:       "2026-07-26__rewe__receipt__groceries.pdf",
	}); err != nil {
		t.Fatal(err)
	}
	count, err := store.Queries.ApprovedExampleCount(t.Context(), sqlc.ApprovedExampleCountParams{
		Sender:         "REWE",
		Recipient:      "alex-example",
		RecipientScope: "personal",
		DocumentType:   "receipt",
		Folder:         "Receipts/Groceries",
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("approval count = %d", count)
	}
}

func TestLearningPersistsRecipientCapacityAndConfiguredAliases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "paperless.sqlite")
	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRecipientProfile(t.Context(), config.RecipientProfile{Name: "Alex Example", Scope: "personal", Aliases: []string{"A Example"}, FolderPrefix: "Private"}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"personal", "sole_proprietor"} {
		if err := store.LearnApproval(t.Context(), Approval{Sender: "merchant", Recipient: "a-example", RecipientScope: scope, DocumentType: "routine-invoice", Folder: "Invoices", Filename: "invoice.pdf"}); err != nil {
			t.Fatal(err)
		}
	}
	store.Close()
	store, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	profiles, err := store.RecipientProfiles(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 2 {
		t.Fatalf("expected one configured personal profile and one learned business profile: %+v", profiles)
	}
	for _, scope := range []string{"personal", "sole_proprietor", "gbr"} {
		n, err := store.Queries.ApprovedExampleCount(t.Context(), sqlc.ApprovedExampleCountParams{Sender: "merchant", Recipient: "a-example", RecipientScope: scope, DocumentType: "routine-invoice", Folder: "Invoices"})
		if err != nil {
			t.Fatal(err)
		}
		want := int64(1)
		if scope == "gbr" {
			want = 0
		}
		if n != want {
			t.Fatalf("%s count=%d", scope, n)
		}
	}
}

func TestLearnedRecipientAliasesSurviveRestartAndDeduplicate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "paperless.sqlite")
	store, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRecipientProfile(t.Context(), config.RecipientProfile{Name: "Alex Example", Scope: "personal", Aliases: []string{"A. Example"}}); err != nil {
		t.Fatal(err)
	}
	profiles, _ := store.RecipientProfiles(t.Context())
	for i, alias := range []string{"Alex Examp1e", "alex-examp1e", "Alex Example", "unknown", ""} {
		id := fmt.Sprintf("alias-%d", i)
		now := Now()
		if err := store.Queries.CreateJob(t.Context(), sqlc.CreateJobParams{ID: id, SourceFilename: "file.pdf", CurrentPath: "file.pdf", Status: "archived", ScanTimestamp: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := store.LearnApproval(t.Context(), Approval{JobID: id, RecipientProfileID: profiles[0].ID, Recipient: "alex-example", RecipientScope: "personal", DetectedRecipient: alias, Folder: "Personal", Filename: "file.pdf"}); err != nil {
			t.Fatal(err)
		}
	}
	store.Close()
	store, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	profiles, err = store.RecipientProfiles(t.Context())
	if err != nil || len(profiles) != 1 || len(profiles[0].Aliases) != 2 || profiles[0].Aliases[1] != "Alex Examp1e" {
		t.Fatalf("aliases=%+v error=%v", profiles, err)
	}
}

func TestConcurrentRawCopiesKeepOneOriginal(t *testing.T) {
	store, err := Open(t.Context(), filepath.Join(t.TempDir(), "paperless.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, id := range []string{"same-file-a", "same-file-b"} {
		if err := store.Queries.CreateJob(t.Context(), sqlc.CreateJobParams{ID: id, SourceFilename: "same.pdf", CurrentPath: "same.pdf", Status: "received", ScanTimestamp: Now(), UpdatedAt: Now()}); err != nil {
			t.Fatal(err)
		}
	}
	results := make(chan error, 2)
	for _, id := range []string{"same-file-a", "same-file-b"} {
		go func() {
			_, err := store.RegisterRawCopy(t.Context(), sqlc.SetRawCopyParams{ID: id, RawPath: id, FileHash: "same-hash", Status: "copying_raw", UpdatedAt: Now()})
			results <- err
		}()
	}
	originals, duplicates := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if errors.Is(err, sql.ErrNoRows) {
			originals++
		} else if err == nil {
			duplicates++
		} else {
			t.Fatal(err)
		}
	}
	if originals != 1 || duplicates != 1 {
		t.Fatalf("originals=%d duplicates=%d", originals, duplicates)
	}
}
