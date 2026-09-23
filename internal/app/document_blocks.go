package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"paperless/internal/classify"
	"paperless/internal/db/sqlc"
	"paperless/internal/document"
	"paperless/internal/ocr"
	"paperless/internal/progress"
	"path/filepath"
	"strings"
	"time"
)

func (p *Processor) documentBlocks(ctx context.Context, job sqlc.Job) (document.Document, error) {
	p.blocksMu.Lock()
	defer p.blocksMu.Unlock()
	d, err := p.store.DocumentBlocks(ctx, job.ID)
	if err == nil && d.Version == document.Version {
		return d, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return d, err
	}
	if !isSafeID(job.ID) {
		return d, fmt.Errorf("invalid document identifier")
	}
	dir := filepath.Join(p.cfg.Paths.Processing, job.ID)
	d, err = ocr.ReadBlockDocument(dir, job.TextPath, int(job.PageCount), 1)
	if err != nil {
		// Compatibility for old archives where only the previously saved Markdown
		// survives. Import it once; subsequent reads use the canonical JSON in SQLite.
		raw, readErr := os.ReadFile(filepath.Join(dir, "document.md"))
		if readErr != nil {
			return d, err
		}
		d = document.Document{Version: document.Version, SourceHash: document.Hash(raw), DistanceMultiplier: 1, Blocks: []document.Block{}}
		for _, part := range strings.Split(string(raw), "\n\n") {
			if text := strings.TrimSpace(part); text != "" {
				d.Blocks = append(d.Blocks, document.Block{ID: len(d.Blocks) + 1, Page: 1, Representation: "paragraph", Content: text})
			}
		}
	}
	if err = p.store.SaveDocumentBlocks(ctx, job.ID, d); err != nil {
		return d, err
	}
	return d, nil
}
func (p *Processor) saveDocumentBlocks(ctx context.Context, jobID string, d document.Document) error {
	p.blocksMu.Lock()
	defer p.blocksMu.Unlock()
	if err := p.store.SaveDocumentBlocks(ctx, jobID, d); err != nil {
		return err
	}
	p.wakeSimilarity()
	return nil
}
func (p *Processor) labelDocumentBlocks(ctx context.Context, d *document.Document, reporter progress.Reporter) {
	defer func() {
		mergeCtx, cancel := context.WithTimeout(ctx, time.Duration(p.cfg.LLM.TimeoutSeconds)*time.Second)
		defer cancel()
		reporter.Info("classify", "unify", "Consolidating document blocks and extracting original-language metadata.", 0, 0, 89)
		unified, err := classify.UnifyDocument(mergeCtx, p.cfg, *d)
		d.Unified = unified
		if err != nil {
			reporter.Warn("classify", "unify", "Metadata extraction unavailable; preserved consolidated text: "+err.Error(), 0, 0, 89)
		}
	}()
	if !p.cfg.LLM.Enabled || p.cfg.LLM.Provider != "bonsai" || len(d.Blocks) == 0 {
		return
	}
	reporter.Info("classify", "blocks", "Classifying document text blocks.", 0, len(d.Blocks), 87)
	labelCtx, cancel := context.WithTimeout(ctx, time.Duration(p.cfg.LLM.TimeoutSeconds)*time.Second)
	defer cancel()
	labels, err := classify.LabelOCRBlocks(labelCtx, p.cfg, d.Blocks)
	if err != nil {
		reporter.Warn("classify", "blocks", "Block labels unavailable; preserving all JSON text: "+err.Error(), 0, len(d.Blocks), 88)
		return
	}
	for i, label := range labels {
		d.Blocks[i].Type = label.Type
		d.Blocks[i].Representation = label.Representation
	}
}
func documentSignature(d document.Document) string {
	data, _ := json.Marshal(d)
	return document.Hash(data)
}
