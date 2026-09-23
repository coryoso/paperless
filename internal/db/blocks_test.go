package db

import (
	"database/sql"
	"errors"
	"paperless/internal/db/sqlc"
	"paperless/internal/document"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestDocumentJSONSurvivesReopenAndResetClearsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "paperless.sqlite")
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Queries.CreateJob(t.Context(), sqlc.CreateJobParams{ID: "doc", Status: "needs_review", SourceFilename: "a.pdf", ScanTimestamp: Now(), UpdatedAt: Now()}); err != nil {
		t.Fatal(err)
	}
	original := document.Document{Version: document.Version, SourceHash: "hash", DistanceMultiplier: 1.25, Blocks: []document.Block{{ID: 4, Page: 2, Type: "subject", Content: "Invitation", Position: &document.Position{X: 12, Y: 34}, Size: &document.Size{Width: 56, Height: 78}}}}
	original.Unified = document.Consolidate(original.Blocks)
	original.Unified.Normalized = document.NormalizeMetadata(original.Unified)
	if err := s.SaveDocumentBlocks(t.Context(), "doc", original); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.DocumentBlocks(t.Context(), "doc")
	if err != nil || !reflect.DeepEqual(got.Unified, original.Unified) || got.DistanceMultiplier != 1.25 || got.Blocks[0].Type != "subject" || *got.Blocks[0].Position != *original.Blocks[0].Position {
		t.Fatal(got, err)
	}
	if err := s.SaveDocumentEmbedding(t.Context(), "doc", "hash", "model", []string{"Invitation"}, [][]float32{{1, 0}}, [][]int{{4, 7}}); err != nil {
		t.Fatal(err)
	}
	var provenance string
	if err := s.Conn().QueryRow(`SELECT source_block_ids FROM embedding_chunks WHERE job_id='doc'`).Scan(&provenance); err != nil || provenance != "[4,7]" {
		t.Fatal(provenance, err)
	}
	var id int
	if err := s.Conn().QueryRow(`SELECT block_id FROM embedding_chunks WHERE job_id='doc'`).Scan(&id); err != nil || id != 4 {
		t.Fatal(id, err)
	}
	if err := s.ResetJobForReprocessing(t.Context(), "doc", "new.pdf"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DocumentBlocks(t.Context(), "doc"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("old document blocks survived reprocessing", err)
	}
}

func TestReplacementConnectionsKeepForeignKeysAndRebuildOrphanChunks(t *testing.T) {
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "with ? characters.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.conn.SetConnMaxLifetime(time.Nanosecond)
	for range 3 {
		var enabled int
		if err := s.conn.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 1 {
			t.Fatal("foreign keys missing on replacement connection", enabled, err)
		}
	}
	s.conn.SetConnMaxLifetime(0)
	if err := s.Queries.CreateJob(t.Context(), sqlc.CreateJobParams{ID: "doc", Status: "needs_review", SourceFilename: "a.pdf", ScanTimestamp: Now(), UpdatedAt: Now()}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveEmbedding(t.Context(), "doc", "old", "model", []string{"old"}, [][]float32{{1, 0}}, []int{1}); err != nil {
		t.Fatal(err)
	}
	// Reproduce a legacy orphan left by a connection that lacked the PRAGMA.
	if _, err := s.conn.ExecContext(t.Context(), "PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.conn.ExecContext(t.Context(), "DELETE FROM document_embeddings WHERE job_id='doc'"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.conn.ExecContext(t.Context(), "PRAGMA foreign_keys=ON"); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveEmbedding(t.Context(), "doc", "new", "model", []string{"new"}, [][]float32{{1, 0}}, []int{2}); err != nil {
		t.Fatal("orphan chunks blocked rebuilding", err)
	}
	var excerpt string
	if err := s.conn.QueryRow("SELECT excerpt FROM embedding_chunks WHERE job_id='doc'").Scan(&excerpt); err != nil || excerpt != "new" {
		t.Fatal(excerpt, err)
	}
}
