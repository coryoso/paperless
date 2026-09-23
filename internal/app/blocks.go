package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"paperless/internal/classify"
	"paperless/internal/ocr"
	"path/filepath"
	"time"
)

func (p *Processor) handleDocumentBlocksAPI(w http.ResponseWriter, r *http.Request) {
	job, err := p.store.Queries.GetJob(r.Context(), r.PathValue("jobID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	d, err := p.documentBlocks(r.Context(), job)
	if err != nil {
		writeAPIError(w, err, 500)
		return
	}
	writeJSON(w, 200, d)
}
func (p *Processor) handleClassifyBlocksAPI(w http.ResponseWriter, r *http.Request) {
	if !localRequest(r) {
		writeAPIError(w, fmt.Errorf("block classification is accepted only from this Mac"), 403)
		return
	}
	job, err := p.store.Queries.GetJob(r.Context(), r.PathValue("jobID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var input struct {
		DistanceMultiplier float64 `json:"distance_multiplier"`
		SourceHash         string  `json:"source_hash"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeAPIError(w, err, 400)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		writeAPIError(w, fmt.Errorf("expected one JSON request"), 400)
		return
	}
	if input.SourceHash == "" || math.IsNaN(input.DistanceMultiplier) || input.DistanceMultiplier < 0 || input.DistanceMultiplier > 4 {
		writeAPIError(w, fmt.Errorf("a source hash and distance multiplier from 0 to 4 are required"), 400)
		return
	}
	select {
	case p.classificationSlots <- struct{}{}:
		defer func() { <-p.classificationSlots }()
	default:
		writeAPIError(w, fmt.Errorf("another classification is running; try again when it finishes"), 409)
		return
	}
	original, err := p.documentBlocks(r.Context(), job)
	if err != nil {
		writeAPIError(w, err, 500)
		return
	}
	if original.SourceHash != input.SourceHash {
		writeAPIError(w, fmt.Errorf("OCR changed; reload the document before classifying"), 409)
		return
	}
	originalSignature := documentSignature(original)
	candidate := original
	candidate.Blocks = append([]classify.OCRBlock(nil), original.Blocks...)
	if input.DistanceMultiplier != original.DistanceMultiplier {
		candidate, err = ocr.ReadBlockDocument(filepath.Join(p.cfg.Paths.Processing, job.ID), job.TextPath, int(job.PageCount), input.DistanceMultiplier)
		if err != nil {
			writeAPIError(w, err, 400)
			return
		}
		if candidate.SourceHash != original.SourceHash {
			writeAPIError(w, fmt.Errorf("OCR changed; reload the document"), 409)
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(p.cfg.LLM.TimeoutSeconds)*time.Second)
	defer cancel()
	labels, err := classify.LabelOCRBlocks(ctx, p.cfg, candidate.Blocks)
	if err != nil {
		writeAPIError(w, err, 502)
		return
	}
	for i, label := range labels {
		candidate.Blocks[i].Type = label.Type
		candidate.Blocks[i].Representation = label.Representation
	}
	candidate.Unified, err = classify.UnifyDocument(ctx, p.cfg, candidate)
	if err != nil {
		writeAPIError(w, err, 502)
		return
	}
	// Reject stale inference after reprocessing or a saved document change.
	p.blocksMu.Lock()
	defer p.blocksMu.Unlock()
	fresh, err := p.store.DocumentBlocks(ctx, job.ID)
	if err != nil || documentSignature(fresh) != originalSignature {
		writeAPIError(w, fmt.Errorf("document changed during classification; reload it"), 409)
		return
	}
	if err := p.store.SaveDocumentBlocks(ctx, job.ID, candidate); err != nil {
		writeAPIError(w, err, 500)
		return
	}
	p.wakeSimilarity()
	writeJSON(w, 200, candidate)
}
