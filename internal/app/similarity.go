package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"paperless/internal/config"
	"paperless/internal/db"
	"paperless/internal/db/sqlc"
	"paperless/internal/document"
	"paperless/internal/embeddings"
)

type similarityState struct {
	Status   string            `json:"status"`
	Message  string            `json:"message"`
	Indexed  int               `json:"indexed"`
	Skipped  int               `json:"skipped"`
	ModelKey string            `json:"-"`
	Failures map[string]string `json:"-"`
}

func (p *Processor) similaritySnapshot() similarityState {
	p.similarityMu.RLock()
	defer p.similarityMu.RUnlock()
	return p.similarity
}
func (p *Processor) setSimilarity(state similarityState) {
	p.similarityMu.Lock()
	defer p.similarityMu.Unlock()
	p.similarity = state
}
func (p *Processor) wakeSimilarity() {
	select {
	case p.similarityWake <- struct{}{}:
	default:
	}
}

func (p *Processor) processSimilarity(ctx context.Context) {
	if !p.cfg.Embeddings.Enabled || p.cfg.NeedsSetup() {
		return
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		p.indexSimilarity(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-p.similarityWake:
		}
	}
}

// Existing archives are backfilled on startup, and new approvals and OCR text
// are discovered without adding inference latency to upload or approval requests.
func (p *Processor) indexSimilarity(ctx context.Context) {
	state := similarityState{Status: "indexing", Message: "Comparing document contents locally."}
	p.setSimilarity(state)
	fail := func(err error) { state.Status = "unavailable"; state.Message = err.Error(); p.setSimilarity(state) }
	client := embeddings.Client{Config: p.cfg.Embeddings}
	model, err := client.Identity(ctx)
	if err != nil {
		fail(err)
		return
	}
	state.ModelKey = model
	p.setSimilarity(state)
	jobs, err := p.store.Queries.ListAllJobs(ctx)
	if err != nil {
		fail(err)
		return
	}
	failures := map[string]string{}
documents:
	for _, job := range jobs {
		if ctx.Err() != nil {
			return
		}
		// Unapproved automatic archives can neither teach nor appear in the panel.
		if job.Status != StatusNeedsReview && !(job.Status == StatusArchived && job.ManualOverride != 0) {
			continue
		}
		blocks, err := p.similarityBlocks(ctx, job)
		if err != nil {
			failures[job.ID] = err.Error()
			if err = p.store.DeleteEmbedding(ctx, job.ID); err != nil {
				fail(err)
				return
			}
			continue
		}
		hash := documentSignature(blocks)
		current, err := p.store.EmbeddingCurrent(ctx, job.ID, hash, model)
		if err != nil {
			fail(err)
			return
		}
		if !current {
			// Remove outdated evidence before inference, including when inference fails.
			if err = p.store.DeleteEmbedding(ctx, job.ID); err != nil {
				fail(err)
				return
			}
			chunks, blockIDs, err := embeddings.UnifiedChunks(blocks)
			if err != nil {
				failures[job.ID] = err.Error()
				continue
			}
			var vectors [][]float32
			for start := 0; start < len(chunks); start += 8 {
				batch, err := client.Embed(ctx, chunks[start:min(start+8, len(chunks))])
				if err != nil {
					var inputErr *embeddings.InputError
					if errors.As(err, &inputErr) {
						failures[job.ID] = inputErr.Error()
						continue documents
					}
					fail(err)
					return
				}
				vectors = append(vectors, batch...)
			}
			latest, err := client.Identity(ctx)
			if err != nil {
				fail(err)
				return
			}
			if latest != model {
				fail(errors.New("The embedding model changed. The index will rebuild on the next pass."))
				return
			}

			// Hold the same short lock used by saved JSON changes through index commit.
			p.blocksMu.Lock()
			fresh, err := p.store.DocumentBlocks(ctx, job.ID)
			if err != nil || documentSignature(fresh) != hash {
				p.blocksMu.Unlock()
				continue
			}
			err = p.store.SaveDocumentEmbedding(ctx, job.ID, hash, model, chunks, vectors, blockIDs)
			p.blocksMu.Unlock()
			if err != nil {
				fail(err)
				return
			}
		}
		state.Indexed++
		p.setSimilarity(state)
	}
	state.Status = "ready"
	state.Message = ""
	state.Skipped = len(failures)
	state.Failures = failures
	p.setSimilarity(state)
}

func (p *Processor) similarityBlocks(ctx context.Context, job sqlc.Job) (document.Document, error) {
	blocks, err := p.documentBlocks(ctx, job)
	if err != nil {
		return blocks, errors.New("Document JSON is not available for similarity search.")
	}
	if len(blocks.Blocks) == 0 {
		return blocks, errors.New("Document text is empty.")
	}
	return blocks, nil
}

func (p *Processor) handleSimilarDocumentsAPI(w http.ResponseWriter, r *http.Request) {
	job, err := p.store.Queries.GetJob(r.Context(), r.PathValue("jobID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	response := struct {
		Status  string              `json:"status"`
		Message string              `json:"message"`
		Matches []db.EmbeddingMatch `json:"matches"`
	}{Matches: []db.EmbeddingMatch{}}
	if !p.cfg.Embeddings.Enabled {
		response.Status = "disabled"
		response.Message = "Enable local document similarity in Setup to compare previously approved documents."
		writeJSON(w, 200, response)
		return
	}
	state := p.similaritySnapshot()
	response.Status = state.Status
	response.Message = state.Message
	if state.Status == "unavailable" {
		writeJSON(w, 200, response)
		return
	}
	if message := state.Failures[job.ID]; message != "" {
		response.Status = "no_text"
		response.Message = message
		writeJSON(w, 200, response)
		return
	}
	blocks, err := p.similarityBlocks(r.Context(), job)
	if err != nil {
		response.Status = "no_text"
		response.Message = err.Error()
		writeJSON(w, 200, response)
		return
	}
	current, err := p.store.EmbeddingCurrent(r.Context(), job.ID, documentSignature(blocks), state.ModelKey)
	if err != nil {
		writeAPIError(w, err, 500)
		return
	}
	if !current || state.ModelKey == "" {
		response.Status = "indexing"
		response.Message = "Preparing document similarities. You can continue reviewing."
		p.wakeSimilarity()
		writeJSON(w, 200, response)
		return
	}
	searchCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	matches, err := p.store.SimilarDocuments(searchCtx, job.ID, state.ModelKey)
	if err != nil {
		writeAPIError(w, err, 500)
		return
	}
	response.Matches = matches
	if response.Status == "" {
		response.Status = "indexing"
	}
	writeJSON(w, 200, response)
}

func (p *Processor) handleEmbeddingSetupAPI(w http.ResponseWriter, r *http.Request) {
	if !localRequest(r) {
		writeAPIError(w, errors.New("setup changes are accepted only from this Mac"), 403)
		return
	}
	var input struct {
		Enabled  bool   `json:"enabled"`
		Endpoint string `json:"endpoint"`
		Model    string `json:"model"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2048))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeAPIError(w, err, 400)
		return
	}
	cfg, err := config.Load(p.configPath)
	if err != nil {
		writeAPIError(w, err, 500)
		return
	}
	cfg.Embeddings.Enabled = input.Enabled
	if input.Endpoint != "" {
		cfg.Embeddings.Endpoint = strings.TrimSpace(input.Endpoint)
	}
	if input.Model != "" {
		cfg.Embeddings.Model = strings.TrimSpace(input.Model)
	}
	cfg, err = cfg.Resolve()
	if err != nil {
		writeAPIError(w, err, 400)
		return
	}
	if input.Enabled {
		client := embeddings.Client{Config: cfg.Embeddings}
		if _, err = client.Identity(r.Context()); err == nil {
			_, err = client.Embed(r.Context(), []string{"Document similarity / Ähnliche Dokumente"})
		}
		if err != nil {
			writeAPIError(w, fmt.Errorf("could not enable document similarity: %w", err), 400)
			return
		}
	}
	if _, err = config.Write(p.configPath, cfg); err != nil {
		writeAPIError(w, err, 500)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"restarting": true})
	p.requestRestart()
}
