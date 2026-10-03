package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"paperless/internal/config"
	"paperless/internal/ocr"
	"paperless/internal/progress"
)

func TestInboxChecksBatchStabilityTogether(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cfg := testServerConfig(t.TempDir())
		cfg.Service.FileStabilitySeconds = 10
		p, cleanup, err := newProcessor(t.Context(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer cleanup()
		for _, name := range []string{"stable.pdf", "stable.png", "changing.pdf", "removed.pdf"} {
			if err := os.WriteFile(filepath.Join(cfg.Paths.Inbox, name), []byte(name), 0600); err != nil {
				t.Fatal(err)
			}
		}
		start := time.Now()
		done := make(chan struct{})
		var count int
		var scanErr error
		go func() {
			defer close(done)
			count, scanErr = p.queueInboxOnce(t.Context(), p.processingQueue)
		}()
		synctest.Wait() // The scan has recorded every file and is waiting for stability.
		if err := os.WriteFile(filepath.Join(cfg.Paths.Inbox, "changing.pdf"), []byte("still being written"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(cfg.Paths.Inbox, "removed.pdf")); err != nil {
			t.Fatal(err)
		}
		<-done
		if scanErr != nil || count != 2 {
			t.Fatalf("queued %d files, want only two stable files: %v", count, scanErr)
		}
		if elapsed := time.Since(start); elapsed != 10*time.Second {
			t.Fatalf("batch took %v, want one stability interval", elapsed)
		}
		for range count {
			work := <-p.processingQueue
			if name := filepath.Base(work.inputPath); name != "stable.pdf" && name != "stable.png" {
				t.Fatalf("queued unstable file %s", name)
			}
		}
	})
}

func TestInboxQueueKeepsDiscoveringWithoutDuplicateJobs(t *testing.T) {
	cfg := testServerConfig(t.TempDir())
	cfg.Service.FileStabilitySeconds = 0
	p, cleanup, err := newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{}, 5)
	release := make(chan struct{})
	var active, peak atomic.Int32
	p.processOCR = func(ctx context.Context, _ config.Config, _, _ string, _ progress.Reporter) (ocr.Result, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		started <- struct{}{}
		select {
		case <-ctx.Done():
			return ocr.Result{}, ctx.Err()
		case <-release:
			return ocr.Result{}, errors.New("test OCR failure")
		}
	}
	writeInput := func(i int) {
		t.Helper()
		name := fmt.Sprintf("scan-%d.pdf", i)
		if err := os.WriteFile(filepath.Join(cfg.Paths.Inbox, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	checkScan := func(want int) {
		t.Helper()
		if count, err := p.queueInboxOnce(ctx, p.processingQueue); err != nil || count != want {
			t.Fatalf("queued %d files, want %d: %v", count, want, err)
		}
	}
	for i := range 4 {
		writeInput(i)
	}
	checkScan(4)
	checkScan(0) // Files still in the inbox must not be queued a second time.
	done := make(chan struct{})
	go func() { defer close(done); p.processQueue(ctx, p.processingQueue) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("workers did not stop")
		}
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("inbox OCR did not start concurrently")
		}
	}
	writeInput(4)
	checkScan(1) // Discovery must continue while the original batch is blocked.
	checkScan(0)
	close(p.processingQueue)
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("queue did not drain after OCR failures")
	}
	if peak.Load() != 2 {
		t.Fatalf("expected peak OCR concurrency 2, got %d", peak.Load())
	}
	jobs, err := p.store.Queries.ListAllJobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 5 {
		t.Fatalf("expected one job per file, got %d", len(jobs))
	}
	for _, job := range jobs {
		if job.Status != StatusFailed {
			t.Fatalf("job %s did not finish: %s", job.ID, job.Status)
		}
	}
	if len(p.inboxPending) != 0 {
		t.Fatal("finished jobs still marked pending")
	}
}

func TestProcessInboxOnceWaitsForConcurrentJobs(t *testing.T) {
	cfg := testServerConfig(t.TempDir())
	cfg.Service.FileStabilitySeconds = 0
	cfg.LLM.Enabled = false
	p, cleanup, err := newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	started := make(chan struct{})
	var arrivals atomic.Int32
	p.processOCR = func(ctx context.Context, _ config.Config, _, workDir string, _ progress.Reporter) (ocr.Result, error) {
		// Both documents must reach OCR before either can finish.
		if arrivals.Add(1) == 2 {
			close(started)
		}
		select {
		case <-ctx.Done():
			return ocr.Result{}, ctx.Err()
		case <-started:
		}
		if err := os.MkdirAll(workDir, 0700); err != nil {
			return ocr.Result{}, err
		}
		pdf, txt := filepath.Join(workDir, "searchable.pdf"), filepath.Join(workDir, "ocr.txt")
		if err := os.WriteFile(pdf, []byte("converted PDF"), 0600); err != nil {
			return ocr.Result{}, err
		}
		if err := os.WriteFile(txt, []byte("Merchant receipt"), 0600); err != nil {
			return ocr.Result{}, err
		}
		return ocr.Result{SearchablePDF: pdf, TextPath: txt, Text: "Merchant receipt", PageCount: 1}, nil
	}
	for i := range 2 {
		name := fmt.Sprintf("scan-%d.pdf", i)
		if err := os.WriteFile(filepath.Join(cfg.Paths.Inbox, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if count, err := p.ProcessInboxOnce(ctx); err != nil || count != 2 {
		t.Fatalf("processed %d files, want 2: %v", count, err)
	}
	jobs, err := p.store.Queries.ListAllJobs(ctx)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("expected two completed jobs: %v, %v", jobs, err)
	}
	for _, job := range jobs {
		if job.Status != StatusNeedsReview {
			t.Fatalf("job %s ended at %s: %s", job.ID, job.Status, job.Error)
		}
	}
	if count, err := p.ProcessInboxOnce(ctx); err != nil || count != 0 {
		t.Fatalf("second pass processed %d files: %v", count, err)
	}
}
