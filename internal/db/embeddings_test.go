package db

import (
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
			if err := store.LearnApproval(ctx, Approval{JobID: id, Recipient: "Alex", RecipientScope: "personal", Folder: "Insurance", Filename: id + ".pdf"}); err != nil {
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

func TestSimilarityLargeArchive(t *testing.T) {
	store, err := Open(t.Context(), filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// Ten passages per document, using the default model's 768 dimensions.
	// Put the closest approved document last in ID order to catch arbitrary caps.
	for _, id := range []string{"query", "far", "z-nearest"} {
		status := "archived"
		if id == "query" {
			status = "needs_review"
		}
		if err := store.Queries.CreateJob(t.Context(), sqlc.CreateJobParams{ID: id, SourceFilename: id + ".pdf", Status: status, ScanTimestamp: Now(), UpdatedAt: Now()}); err != nil {
			t.Fatal(err)
		}
		if id != "query" {
			if err := store.LearnApproval(t.Context(), Approval{JobID: id, Folder: "Archive", Filename: id + ".pdf"}); err != nil {
				t.Fatal(err)
			}
		}
		chunks := make([]string, 10)
		vectors := make([][]float32, 10)
		for i := range vectors {
			chunks[i] = fmt.Sprintf("%s section %d: %s", id, i, strings.Repeat("Document paragraph. ", 55))
			vectors[i] = make([]float32, 768)
			vectors[i][0] = 1
			if id == "far" {
				vectors[i][0] = 0
				vectors[i][1] = 1
			}
			vectors[i][i+2] = .01
		}
		if err := store.SaveEmbedding(t.Context(), id, "hash", "model", chunks, vectors); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := store.conn.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := tx.ExecContext(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2998; i++ {
		id := fmt.Sprintf("doc-%04d", i)
		exec(`INSERT INTO jobs(id,source_filename,scan_timestamp,updated_at,status) VALUES(?,?,'','','archived')`, id, id+".pdf")
		exec(`INSERT INTO routing_examples(created_at,source_job_id,folder,filename) VALUES('',?,'Archive',?)`, id, id+".pdf")
		exec(`INSERT INTO document_embeddings(job_id,content_hash,model_key,dimensions,indexed_at,centroid) SELECT ?,content_hash,model_key,dimensions,indexed_at,centroid FROM document_embeddings WHERE job_id='far'`, id)
		exec(`INSERT INTO embedding_chunks(job_id,ordinal,excerpt,vector) SELECT ?,ordinal,excerpt,vector FROM embedding_chunks WHERE job_id='far'`, id)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	start := time.Now()
	matches, err := store.SimilarDocuments(ctx, "query", "model")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 5 || matches[0].JobID != "z-nearest" || math.Abs(matches[0].Distance) > .0001 {
		t.Fatalf("bad matches: %+v", matches)
	}
	t.Logf("3,000 approved documents: %s", time.Since(start))
}

func TestSimilarityRebuildsCacheWithoutCentroid(t *testing.T) {
	store, err := Open(t.Context(), filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Queries.CreateJob(t.Context(), sqlc.CreateJobParams{ID: "old", SourceFilename: "old.pdf", Status: "needs_review", ScanTimestamp: Now(), UpdatedAt: Now()}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveEmbedding(t.Context(), "old", "hash", "model", []string{"section"}, [][]float32{{1, 0}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.conn.Exec(`UPDATE document_embeddings SET centroid=NULL WHERE job_id='old'`); err != nil {
		t.Fatal(err)
	}
	current, err := store.EmbeddingCurrent(t.Context(), "old", "hash", "model")
	if err != nil || current {
		t.Fatal("old cache did not request rebuild", current, err)
	}
	if err := store.SaveEmbedding(t.Context(), "old", "hash", "model", []string{"section"}, [][]float32{{1, 0}}); err != nil {
		t.Fatal(err)
	}
	current, err = store.EmbeddingCurrent(t.Context(), "old", "hash", "model")
	if err != nil || !current {
		t.Fatal("rebuilt cache not current", current, err)
	}
}
