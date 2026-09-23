package db

import (
	"encoding/json"
	"paperless/internal/classify"
	"paperless/internal/db/sqlc"
	"paperless/internal/document"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRemoveCategoryAndBackfillNormalizedFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.sqlite")
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	// Restore the prior schema to exercise migration on populated records.
	for _, q := range []string{`ALTER TABLE routing_examples ADD COLUMN document_type TEXT NOT NULL DEFAULT ''`, `CREATE INDEX idx_routing_sender_type ON routing_examples(sender,document_type)`, `CREATE INDEX routing_examples_scope ON routing_examples(sender,recipient,recipient_scope,document_type,folder)`, `DROP INDEX routing_examples_identity`, `DELETE FROM schema_migrations WHERE version='0009_remove_document_type.sql'`} {
		if _, err = s.conn.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	s.Queries.CreateJob(t.Context(), sqlc.CreateJobParams{ID: "metadata", SourceFilename: "original.pdf", Status: "needs_review", ScanTimestamp: Now(), UpdatedAt: Now()})
	s.conn.Exec(`UPDATE jobs SET classification_json='{"document_type":"letter","recipient":"alex-example","recipient_scope":"personal"}' WHERE id='metadata'`)
	blocks := []document.Block{{ID: 1, Type: "recipient", Content: "Herrn ALEX EXAMPLE\nEXAMPLE ROAD 1\n12345 BERLIN"}}
	u := document.Consolidate(blocks)
	u.Date = "2026-08-11"
	u.Subject = "Einladung"
	u.Recipient = document.Party{Names: []string{"Herrn ALEX EXAMPLE"}, Addresses: []string{"EXAMPLE ROAD 1", "12345 BERLIN"}, SourceIDs: []int{1}}
	if err = s.SaveDocumentBlocks(t.Context(), "metadata", document.Document{Version: 1, Blocks: blocks, Unified: u}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var schema string
	s.conn.QueryRow(`SELECT sql FROM sqlite_master WHERE name='routing_examples'`).Scan(&schema)
	if strings.Contains(schema, "document_type") {
		t.Fatal(schema)
	}
	job, err := s.Queries.GetJob(t.Context(), "metadata")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(job.ClassificationJson, "document_type") {
		t.Fatal(job.ClassificationJson)
	}
	var c classify.Classification
	json.Unmarshal([]byte(job.ClassificationJson), &c)
	if c.Metadata == nil || c.Metadata.Recipient.Names[0] != "Alex Example" {
		t.Fatal(c)
	}
	saved, err := s.DocumentBlocks(t.Context(), "metadata")
	if err != nil || !reflect.DeepEqual(saved.Blocks, blocks) || saved.Unified.Recipient.Names[0] != "Herrn ALEX EXAMPLE" {
		t.Fatal(saved, err)
	}
	// A confirmed profile updates the normalized projection atomically, not OCR.
	c.Metadata.Recipient.Names = []string{"Alexandra Example"}
	c.Metadata.Recipient.ProfileID = 7
	c.Metadata.Recipient.Origin = "profile"
	raw, _ := json.Marshal(c)
	if err = s.SaveReviewedClassification(t.Context(), sqlc.SetClassifiedParams{ID: "metadata", ClassificationJson: string(raw), Status: "needs_review", UpdatedAt: Now()}, c.Metadata); err != nil {
		t.Fatal(err)
	}
	saved, err = s.DocumentBlocks(t.Context(), "metadata")
	if err != nil || saved.Unified.Normalized.Recipient.ProfileID != 7 || !reflect.DeepEqual(saved.Blocks, blocks) {
		t.Fatal(saved, err)
	}
}

func TestMetadataUpgradePreservesReviewedRecipient(t *testing.T) {
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.Queries.CreateJob(t.Context(), sqlc.CreateJobParams{ID: "reviewed", Status: "needs_review"}); err != nil {
		t.Fatal(err)
	}
	u := document.Consolidate([]document.Block{{ID: 1, Type: "recipient", Content: "Original"}})
	u.Normalized = &document.Metadata{Version: 1, Recipient: document.Identity{Origin: "review", Names: []string{"Reviewed Person"}, Addresses: []document.PostalAddress{{Lines: []string{"Example Road 4", "01234 Example City"}}}}}
	if err = s.SaveDocumentBlocks(t.Context(), "reviewed", document.Document{Version: 1, Unified: u}); err != nil {
		t.Fatal(err)
	}
	if err = s.BackfillMetadata(t.Context()); err != nil {
		t.Fatal(err)
	}
	saved, err := s.DocumentBlocks(t.Context(), "reviewed")
	if err != nil {
		t.Fatal(err)
	}
	identity := saved.Unified.Normalized.Recipient
	if identity.Names[0] != "Reviewed Person" || identity.Origin != "review" || identity.Addresses[0].PostalCode != "01234" {
		t.Fatal(identity)
	}
}
