package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"paperless/internal/config"
	"paperless/internal/db"
	"paperless/internal/db/sqlc"
)

func TestSimilarityBackfillCachingAndProviderIndependence(t *testing.T) {
	var calls atomic.Int32
	digest := "weights-v1"
	available := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !available {
			http.Error(w, "offline", 503)
			return
		}
		if r.URL.Path == "/api/tags" {
			json.NewEncoder(w).Encode(map[string]any{"models": []map[string]string{{"name": "embeddinggemma:latest", "digest": digest}}})
			return
		}
		calls.Add(1)
		var input struct {
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&input)
		vectors := make([][]float32, len(input.Input))
		for i := range vectors {
			vectors[i] = []float32{1, 0}
		}
		json.NewEncoder(w).Encode(map[string]any{"embeddings": vectors})
	}))
	defer server.Close()
	cfg := testServerConfig(t.TempDir())
	cfg.LLM.Provider = "fm"
	cfg.Embeddings.Enabled = true
	cfg.Embeddings.Endpoint = server.URL
	p, cleanup, err := newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	create := func(id, status string, approved bool) {
		if err := p.store.Queries.CreateJob(t.Context(), sqlc.CreateJobParams{ID: id, Status: status, SourceFilename: id + ".pdf", ScanTimestamp: db.Now(), UpdatedAt: db.Now()}); err != nil {
			t.Fatal(err)
		}
		if approved {
			if _, err := p.store.Conn().Exec(`UPDATE jobs SET manual_override=1 WHERE id=?`, id); err != nil {
				t.Fatal(err)
			}
			if err := p.store.LearnApproval(t.Context(), db.Approval{JobID: id, Folder: "Insurance", Filename: id + ".pdf", Recipient: "Alex", RecipientScope: "personal"}); err != nil {
				t.Fatal(err)
			}
		}
		dir := filepath.Join(cfg.Paths.Processing, id)
		os.MkdirAll(dir, 0700)
		os.WriteFile(filepath.Join(dir, "document.md"), []byte("# Insurance renewal\n\nAnnual coverage."), 0600)
	}
	create("old-approved", "archived", true)
	create("new-review", "needs_review", false)
	create("automatic", "archived", false)
	p.indexSimilarity(t.Context())
	if calls.Load() != 2 || p.similaritySnapshot().Status != "ready" {
		t.Fatal(calls.Load(), p.similaritySnapshot())
	}
	p.indexSimilarity(t.Context())
	if calls.Load() != 2 {
		t.Fatal("cached documents embedded again")
	}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.SetPathValue("jobID", "new-review")
	p.handleSimilarDocumentsAPI(recorder, req)
	if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), "old-approved") || strings.Contains(recorder.Body.String(), "automatic") {
		t.Fatal(recorder.Body.String())
	}
	os.WriteFile(filepath.Join(cfg.Paths.Processing, "new-review", "document.md"), []byte("Changed content."), 0600)
	p.indexSimilarity(t.Context())
	if calls.Load() != 3 {
		t.Fatal("content change did not rebuild", calls.Load())
	}
	digest = "weights-v2"
	p.indexSimilarity(t.Context())
	if calls.Load() != 5 {
		t.Fatal("model replacement did not rebuild", calls.Load())
	}
	available = false
	p.indexSimilarity(t.Context())
	if p.similaritySnapshot().Status != "unavailable" {
		t.Fatal("outage not surfaced")
	}
	available = true
	p.indexSimilarity(t.Context())
	if p.similaritySnapshot().Status != "ready" {
		t.Fatal("did not recover")
	}
	os.Remove(filepath.Join(cfg.Paths.Processing, "old-approved", "document.md"))
	p.indexSimilarity(t.Context())
	matches, err := p.store.SimilarDocuments(t.Context(), "new-review", p.similaritySnapshot().ModelKey)
	if err != nil || len(matches) != 0 {
		t.Fatal("stale evidence retained", matches, err)
	}
}

func TestEmbeddingSetupKeepsClassifierAndRejectsRemoteEndpoint(t *testing.T) {
	cfg := testServerConfig(t.TempDir())
	cfg.LLM.Provider = "bonsai"
	path := filepath.Join(t.TempDir(), "config.toml")
	if _, err := config.Write(path, cfg); err != nil {
		t.Fatal(err)
	}
	p, cleanup, err := newProcessorAtPath(t.Context(), cfg, path)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "http://127.0.0.1/api/setup/embeddings", strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:4321"
		w := httptest.NewRecorder()
		p.handleEmbeddingSetupAPI(w, req)
		return w
	}
	if w := post(`{"enabled":true,"endpoint":"https://example.com","model":"embeddinggemma"}`); w.Code != 400 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := post(`{"enabled":false,"endpoint":"http://127.0.0.1:11434","model":"embeddinggemma"}`); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	saved, err := config.Load(path)
	if err != nil || saved.LLM.Provider != "bonsai" || saved.Embeddings.Enabled {
		t.Fatal(saved, err)
	}
	req := httptest.NewRequest("POST", "/api/setup/embeddings", strings.NewReader(`{"enabled":false}`))
	req.RemoteAddr = "192.0.2.1:4567"
	w := httptest.NewRecorder()
	p.handleEmbeddingSetupAPI(w, req)
	if w.Code != 403 {
		t.Fatal(w.Code)
	}
}

func TestSimilaritySkipsRejectedDocumentAndRecovers(t *testing.T) {
	var reject atomic.Bool
	reject.Store(true)
	var healthyCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			json.NewEncoder(w).Encode(map[string]any{"models": []map[string]string{{"name": "embeddinggemma:latest", "digest": "test"}}})
			return
		}
		var input struct {
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&input)
		if strings.Contains(input.Input[0], "rejected") && reject.Load() {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"the input length exceeds the context length"}`))
			return
		}
		healthyCalls.Add(1)
		json.NewEncoder(w).Encode(map[string]any{"embeddings": [][]float32{{1, 0}}})
	}))
	defer server.Close()
	cfg := testServerConfig(t.TempDir())
	cfg.Embeddings.Enabled = true
	cfg.Embeddings.Endpoint = server.URL
	p, cleanup, err := newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for i, id := range []string{"rejected", "healthy-document"} {
		stamp := "2026-09-20T00:00:00Z"
		if i == 1 {
			stamp = "2026-09-19T00:00:00Z"
		}
		if err := p.store.Queries.CreateJob(t.Context(), sqlc.CreateJobParams{ID: id, Status: StatusNeedsReview, SourceFilename: id + ".pdf", ScanTimestamp: stamp, UpdatedAt: stamp}); err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(cfg.Paths.Processing, id)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "document.md"), []byte(id), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		p.indexSimilarity(t.Context())
		state := p.similaritySnapshot()
		if state.Status != "ready" || state.Indexed != 1 || state.Skipped != 1 || state.Failures["rejected"] == "" {
			t.Fatalf("rejected document blocked healthy indexing: %+v", state)
		}
	}
	if healthyCalls.Load() != 1 {
		t.Fatalf("healthy document not cached: calls=%d", healthyCalls.Load())
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.SetPathValue("jobID", "rejected")
	w := httptest.NewRecorder()
	p.handleSimilarDocumentsAPI(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"no_text"`) || !strings.Contains(w.Body.String(), "context limit") {
		t.Fatal(w.Code, w.Body.String())
	}
	reject.Store(false)
	p.indexSimilarity(t.Context())
	state := p.similaritySnapshot()
	if state.Status != "ready" || state.Indexed != 2 || state.Skipped != 0 || len(state.Failures) != 0 {
		t.Fatalf("corrected document did not recover: %+v", state)
	}
}
