package db

import (
	"math"
	"path/filepath"
	"testing"

	"paperless/internal/db/sqlc"
)

func TestSemanticSearchApprovalIsolationAndLifecycle(t *testing.T) {
	ctx := t.Context()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	create := func(id, status, model string, approved bool, vectors [][]float32) {
		t.Helper()
		if err := store.Queries.CreateJob(ctx, sqlc.CreateJobParams{ID: id, SourceFilename: id + ".pdf", Status: status, ScanTimestamp: Now(), UpdatedAt: Now()}); err != nil {
			t.Fatal(err)
		}
		if approved {
			if err := store.LearnApproval(ctx, Approval{JobID: id, Recipient: "Alex", RecipientScope: "personal", Folder: "Insurance", Filename: id + ".pdf", DocumentType: "contract"}); err != nil {
				t.Fatal(err)
			}
		}
		chunks := make([]string, len(vectors))
		for i := range chunks {
			chunks[i] = "excerpt " + id
		}
		if err := store.SaveEmbedding(ctx, id, "hash", model, chunks, vectors); err != nil {
			t.Fatal(err)
		}
	}
	create("query", "needs_review", "model-v1", false, [][]float32{{1, 0}})
	create("approved", "archived", "model-v1", true, [][]float32{{.9, .1}, {1, 0}})
	create("automatic", "archived", "model-v1", false, [][]float32{{1, 0}})
	create("unreviewed", "needs_review", "model-v1", false, [][]float32{{1, 0}})
	create("other-model", "archived", "model-v2", true, [][]float32{{1, 0}})
	create("other-dimensions", "archived", "model-v1", true, [][]float32{{1, 0, 0}})
	matches, err := store.SimilarDocuments(ctx, "query", "model-v1")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].JobID != "approved" || math.Abs(matches[0].Distance) > .0001 || matches[0].QueryExcerpt != "excerpt query" {
		t.Fatalf("matches=%+v", matches)
	}
	current, err := store.EmbeddingCurrent(ctx, "query", "hash", "model-v1")
	if err != nil || !current {
		t.Fatal(current, err)
	}
	current, _ = store.EmbeddingCurrent(ctx, "query", "changed", "model-v1")
	if current {
		t.Fatal("stale content accepted")
	}
	// Failed replacement must preserve the old index atomically.
	err = store.SaveEmbedding(ctx, "approved", "changed", "model-v1", []string{"invalid"}, [][]float32{{float32(math.NaN()), 0}})
	if err == nil {
		t.Fatal("accepted NaN")
	}
	matches, err = store.SimilarDocuments(ctx, "query", "model-v1")
	if err != nil || len(matches) != 1 {
		t.Fatal(matches, err)
	}
	snapshot := filepath.Join(t.TempDir(), "backup.sqlite")
	if err = store.Snapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if err = IntegrityCheck(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	backup, err := Open(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	if matches, err = backup.SimilarDocuments(ctx, "query", "model-v1"); err != nil || len(matches) != 1 {
		t.Fatal(matches, err)
	}
	if err = store.DeleteJob(ctx, "approved"); err != nil {
		t.Fatal(err)
	}
	matches, err = store.SimilarDocuments(ctx, "query", "model-v1")
	if err != nil || len(matches) != 0 {
		t.Fatal(matches, err)
	}
	var count int
	if err = store.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM embedding_chunks WHERE job_id='approved'`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
