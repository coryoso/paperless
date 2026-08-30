package db

import (
	"path/filepath"
	"testing"

	"paperless/internal/db/sqlc"
)

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
		JobID:        "",
		Sender:       "rewe",
		Recipient:    "alex-example",
		DocumentType: "receipt",
		Folder:       "Receipts/Groceries",
		Filename:     "2026-07-26__rewe__receipt__groceries.pdf",
	}); err != nil {
		t.Fatal(err)
	}
	count, err := store.Queries.ApprovedExampleCount(t.Context(), sqlc.ApprovedExampleCountParams{
		Sender:       "REWE",
		Recipient:    "alex-example",
		DocumentType: "receipt",
		Folder:       "Receipts/Groceries",
	})
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("approval count = %d", count)
	}
}
