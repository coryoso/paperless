package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"paperless/internal/db"
	"paperless/internal/db/sqlc"
)

// retryJob restarts the existing record, bypassing ingestion's duplicate check.
// The dashboard workers also run in serve-only mode, without an inbox watcher.
func (p *Processor) retryJob(ctx context.Context, jobID string) (string, error) {
	if !isSafeID(jobID) {
		return "", errors.New("invalid document ID")
	}
	if p.cfg.NeedsSetup() {
		return "", errors.New("finish setup before reprocessing documents")
	}
	p.processingMu.Lock()
	defer p.processingMu.Unlock()
	if len(p.uploadQueue) == cap(p.uploadQueue) {
		return "", errors.New("processing queue is full; try again shortly")
	}
	if attempt := p.activeJobs[jobID]; attempt != nil {
		attempt.cancel()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-attempt.done:
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timer.C:
			return "", errors.New("previous processing is still stopping; try again shortly")
		}
	}
	job, err := p.store.Queries.GetJob(ctx, jobID)
	if err != nil {
		return "", err
	}
	if job.Status == StatusRejected {
		return "", errors.New("deleted documents cannot be reprocessed")
	}
	source := job.RawPath
	if source == "" {
		source = job.CurrentPath
	}
	if source == "" {
		return "", errors.New("document has no source file to reprocess")
	}
	runID := randomID()
	inputDir := filepath.Join(p.cfg.Paths.Processing, "uploads", runID)
	if err := os.MkdirAll(inputDir, 0700); err != nil {
		return "", err
	}
	queued := false
	defer func() {
		if !queued {
			_ = os.RemoveAll(inputDir)
		}
	}()
	inputPath := filepath.Join(inputDir, safeInputName(job.SourceFilename))
	if err := copyFile(source, inputPath); err != nil {
		return "", fmt.Errorf("read original document: %w", err)
	}
	// Clear generated OCR artifacts so pages from an earlier attempt cannot leak
	// into the new preview. The raw original and existing archive file are kept.

	if err := func() error {
		p.blocksMu.Lock()
		defer p.blocksMu.Unlock()
		p.pagesMu.Lock()
		defer p.pagesMu.Unlock()
		if err := os.RemoveAll(filepath.Join(p.cfg.Paths.Processing, job.ID)); err != nil {
			return err
		}
		if _, err := p.store.Conn().ExecContext(ctx, "DELETE FROM document_pages WHERE job_id=?", job.ID); err != nil {
			return err
		}
		return p.store.ResetJobForReprocessing(ctx, job.ID, inputPath)
	}(); err != nil {
		return "", err
	}

	p.notifyDashboard()
	scanTime, err := time.Parse(time.RFC3339, job.ScanTimestamp)
	if err != nil {
		scanTime = time.Now()
	}
	state := p.runs.create(runID)
	state.publish(progressEvent("prepare", "queued", "Reprocessing queued; results will return to Review.", 6))
	select {
	case p.uploadQueue <- uploadWork{jobID: job.ID, uploadPath: inputPath, scanTime: scanTime, state: state, reprocess: true}:
		queued = true
	default:
		err := errors.New("processing queue is full; try again shortly")
		_ = p.failJob(context.WithoutCancel(ctx), job.ID, err)
		state.finish(err)
		// Keep the new source available for another retry, including jobs without raw copies.
		queued = true
		return "", err
	}
	// A job interrupted before ingestion moved its source can still be in the
	// watched inbox. Its replacement is now safely queued outside that folder.
	if inboxSource, err := validateJobArtifact(jobArtifact{path: job.CurrentPath, roots: []string{p.cfg.Paths.Inbox}}); err == nil && inboxSource != "" {
		if err := removeJobArtifact(inboxSource, false); err != nil {
			slog.Warn("could not remove superseded inbox input", "job", job.ID, "error", err)
		}
	}
	_ = p.store.Queries.AddEvent(ctx, sqlc.AddEventParams{JobID: job.ID, CreatedAt: db.Now(), Level: "info", Message: "reprocessing requested"})
	return runID, nil
}
