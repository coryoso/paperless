package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"paperless/internal/config"
	"paperless/internal/ocr"
	"paperless/internal/progress"
)

func TestIngestionPreservesInputFormatUntilOCR(t *testing.T) {
	for _, extension := range []string{".png", ".jpg", ".JPEG", ".pdf"} {
		t.Run(extension, func(t *testing.T) {
			cfg := testServerConfig(t.TempDir())
			cfg.LLM.Enabled = false
			p, cleanup, err := newProcessor(t.Context(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer cleanup()
			input := filepath.Join(cfg.Paths.Inbox, "My Scan"+extension)
			original := []byte("original input bytes")
			if err := os.WriteFile(input, original, 0600); err != nil {
				t.Fatal(err)
			}
			p.processOCR = func(_ context.Context, _ config.Config, input, workDir string, _ progress.Reporter) (ocr.Result, error) {
				if filepath.Ext(input) != strings.ToLower(extension) {
					return ocr.Result{}, fmt.Errorf("input format changed before OCR: %s", input)
				}
				if err := os.MkdirAll(workDir, 0700); err != nil {
					return ocr.Result{}, err
				}
				pdf, txt := filepath.Join(workDir, "searchable.pdf"), filepath.Join(workDir, "ocr.txt")
				if err := os.WriteFile(pdf, []byte("converted PDF"), 0600); err != nil {
					return ocr.Result{}, err
				}
				if err := os.WriteFile(txt, []byte("Merchant invoice"), 0600); err != nil {
					return ocr.Result{}, err
				}
				return ocr.Result{SearchablePDF: pdf, TextPath: txt, Text: "Merchant invoice", PageCount: 1}, nil
			}
			id, err := p.ProcessUploadedFile(t.Context(), "", input, nil)
			if err != nil {
				t.Fatal(err)
			}
			job, err := p.store.Queries.GetJob(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			if job.Status != StatusNeedsReview || filepath.Ext(job.CurrentPath) != ".pdf" || filepath.Ext(job.RawPath) != strings.ToLower(extension) {
				t.Fatalf("unexpected pipeline artifacts: %+v", job)
			}
			raw, err := os.ReadFile(job.RawPath)
			if err != nil || string(raw) != string(original) {
				t.Fatalf("original input was not preserved: %q, %v", raw, err)
			}
		})
	}
}

func TestUploadQueueOverlapsOCRBeforeClassification(t *testing.T) {
	cfg := testServerConfig(t.TempDir())
	cfg.LLM.Enabled = false
	cfg.OCR.Workers = 2
	p, cleanup, err := newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan string, 4)
	release := make(chan struct{}, 4)
	var active, peak atomic.Int32
	p.processOCR = func(ctx context.Context, _ config.Config, input, workDir string, _ progress.Reporter) (ocr.Result, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		started <- input
		select {
		case <-ctx.Done():
			return ocr.Result{}, ctx.Err()
		case <-release:
		}
		if err := os.MkdirAll(workDir, 0700); err != nil {
			return ocr.Result{}, err
		}
		pdf, txt := filepath.Join(workDir, "searchable.pdf"), filepath.Join(workDir, "ocr.txt")
		if err := os.WriteFile(pdf, []byte("searchable document"), 0600); err != nil {
			return ocr.Result{}, err
		}
		if err := os.WriteFile(txt, []byte("Merchant receipt"), 0600); err != nil {
			return ocr.Result{}, err
		}
		return ocr.Result{SearchablePDF: pdf, TextPath: txt, Text: "Merchant receipt", PageCount: 1, TextSource: "ocr", InputKind: "scan"}, nil
	}
	// Hold the model stage to demonstrate OCR can progress independently.
	p.classificationSlots <- struct{}{}
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("parallel-%d", i)
		path := filepath.Join(cfg.Paths.Inbox, id+".pdf")
		if err := os.WriteFile(path, []byte(id), 0600); err != nil {
			t.Fatal(err)
		}
		info, _ := os.Stat(path)
		state := p.runs.create(id)
		if err := p.createJob(ctx, id, path, info, state.reporter()); err != nil {
			t.Fatal(err)
		}
		p.uploadQueue <- uploadWork{jobID: id, uploadPath: path, scanTime: info.ModTime(), state: state}
	}
	close(p.uploadQueue)
	done := make(chan struct{})
	go func() { defer close(done); p.processUploadQueue(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("workers did not stop")
		}
	}()
	waitStarted := func() {
		t.Helper()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("OCR did not start concurrently")
		}
	}
	waitStarted()
	waitStarted()
	if active.Load() != 2 {
		t.Fatalf("expected two simultaneous OCR jobs, got %d", active.Load())
	}
	release <- struct{}{}
	waitStarted() // Third OCR starts even though classification cannot run yet.
	jobs, err := p.store.Queries.ListAllJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if job.Status == StatusNeedsReview {
			t.Fatal("classification bypassed its gate")
		}
	}
	release <- struct{}{}
	release <- struct{}{}
	<-p.classificationSlots
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("queue did not drain")
	}
	if peak.Load() != 2 {
		t.Fatalf("OCR concurrency exceeded limit: %d", peak.Load())
	}
	jobs, err = p.store.Queries.ListAllJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if job.Status != StatusNeedsReview {
			t.Fatalf("job %s ended at %s: %s", job.ID, job.Status, job.Error)
		}
	}
}

func TestStageWaitCancellationDoesNotConsumeSlot(t *testing.T) {
	slots := make(chan struct{}, 1)
	slots <- struct{}{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := acquireStage(ctx, slots); err == nil {
		t.Fatal("canceled wait succeeded")
	}
	<-slots
	if err := acquireStage(ctx, slots); err == nil {
		t.Fatal("canceled work acquired free slot")
	}
	if len(slots) != 0 {
		t.Fatal("cancellation leaked slot")
	}
	if err := acquireStage(t.Context(), slots); err != nil {
		t.Fatal(err)
	}
	<-slots
}
