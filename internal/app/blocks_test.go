package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"paperless/internal/document"
	"strings"
	"testing"

	"paperless/internal/db"
	"paperless/internal/db/sqlc"
)

func TestBlockAPIRejectsRemoteInvalidAndConcurrentRequests(t *testing.T) {
	p, cleanup, err := newProcessor(t.Context(), testServerConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := p.store.Queries.CreateJob(t.Context(), sqlc.CreateJobParams{ID: "blocks", Status: "needs_review", SourceFilename: "test.pdf", ScanTimestamp: db.Now(), UpdatedAt: db.Now()}); err != nil {
		t.Fatal(err)
	}
	valid := `{"distance_multiplier":1,"source_hash":"source"}`
	p.classificationSlots <- struct{}{}
	defer func() { <-p.classificationSlots }()
	for _, tt := range []struct {
		addr, body string
		status     int
	}{
		{"192.0.2.1:1234", valid, http.StatusForbidden},
		{"127.0.0.1:1234", `{"blocks":[]}`, http.StatusBadRequest},
		{"127.0.0.1:1234", valid + `{}`, http.StatusBadRequest},
		{"127.0.0.1:1234", valid, http.StatusConflict},
	} {
		request := httptest.NewRequest("POST", "/api/jobs/blocks/classify-blocks", strings.NewReader(tt.body))
		request.RemoteAddr = tt.addr
		request.SetPathValue("jobID", "blocks")
		response := httptest.NewRecorder()
		p.handleClassifyBlocksAPI(response, request)
		if response.Code != tt.status {
			t.Fatalf("got %d want %d: %s", response.Code, tt.status, response.Body.String())
		}
	}
}

func TestBlockClassificationPersistsAndRejectsStaleInference(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(fmt.Sprint(stale), func(t *testing.T) {
			cfg := testServerConfig(t.TempDir())
			p, cleanup, err := newProcessor(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			if err := p.store.Queries.CreateJob(t.Context(), sqlc.CreateJobParams{ID: "blocks", Status: StatusNeedsReview, SourceFilename: "test.pdf", ScanTimestamp: db.Now(), UpdatedAt: db.Now()}); err != nil {
				t.Fatal(err)
			}
			original := document.Document{Version: document.Version, SourceHash: "source", DistanceMultiplier: 1, Blocks: []document.Block{{ID: 1, Page: 1, Content: "Library"}}}
			if err := p.saveDocumentBlocks(t.Context(), "blocks", original); err != nil {
				t.Fatal(err)
			}
			if err := p.store.SaveEmbedding(t.Context(), "blocks", "old", "model", []string{"Library"}, [][]float32{{1, 0}}, []int{1}); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/props":
					fmt.Fprint(w, `{"default_generation_settings":{"n_ctx":65536}}`)
				case "/tokenize":
					fmt.Fprint(w, `{"tokens":[1,2]}`)
				default:
					if stale {
						newDoc := original
						newDoc.SourceHash = "new-ocr"
						if err := p.saveDocumentBlocks(t.Context(), "blocks", newDoc); err != nil {
							t.Error(err)
						}
					}
					json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": `{"type":"sender_name_or_address"}`}}}})
				}
			}))
			defer server.Close()
			p.cfg.Bonsai.Endpoint = server.URL
			request := httptest.NewRequest("POST", "/", strings.NewReader(`{"source_hash":"source","distance_multiplier":1}`))
			request.RemoteAddr = "127.0.0.1:1234"
			request.SetPathValue("jobID", "blocks")
			response := httptest.NewRecorder()
			p.handleClassifyBlocksAPI(response, request)
			want := 200
			if stale {
				want = 409
			}
			if response.Code != want {
				t.Fatal(response.Code, response.Body.String())
			}
			saved, err := p.store.DocumentBlocks(t.Context(), "blocks")
			if err != nil {
				t.Fatal(err)
			}
			if !stale && (saved.Blocks[0].Type != "sender" || saved.Blocks[0].Content != "Library" || saved.Blocks[0].Position != nil) {
				t.Fatal(saved)
			}
			if stale && (saved.SourceHash != "new-ocr" || saved.Blocks[0].Type != "") {
				t.Fatal("stale labels were saved", saved)
			}
			current, err := p.store.EmbeddingCurrent(t.Context(), "blocks", "old", "model")
			if err != nil || current {
				t.Fatal("old vectors survived JSON update", err)
			}
		})
	}
}
