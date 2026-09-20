package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"paperless/internal/config"
	"paperless/internal/db"
	"paperless/internal/db/sqlc"
	"paperless/internal/ocr"
	"paperless/internal/progress"
)

func reprocessTestProcessor(t *testing.T) *Processor {
	t.Helper()
	cfg := testServerConfig(t.TempDir())
	cfg.LLM.Enabled = false
	p, cleanup, err := newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	p.processOCR = func(_ context.Context, _ config.Config, input, workDir string, _ progress.Reporter) (ocr.Result, error) {
		data, err := os.ReadFile(input)
		if err != nil {
			return ocr.Result{}, err
		}
		if string(data) != "original image" || filepath.Ext(input) != ".png" {
			t.Errorf("OCR input = %q (%s)", data, input)
		}
		if err := os.MkdirAll(workDir, 0700); err != nil {
			return ocr.Result{}, err
		}
		pdf, txt := filepath.Join(workDir, "searchable.pdf"), filepath.Join(workDir, "ocr.txt")
		if err := os.WriteFile(pdf, []byte("new PDF"), 0600); err != nil {
			return ocr.Result{}, err
		}
		if err := os.WriteFile(txt, []byte("Merchant invoice"), 0600); err != nil {
			return ocr.Result{}, err
		}
		return ocr.Result{SearchablePDF: pdf, TextPath: txt, Text: "Merchant invoice", TextHash: "fresh-text", PageCount: 1, InputKind: "scan", TextSource: "ocr"}, nil
	}
	return p
}

func createReprocessTestJob(t *testing.T, p *Processor, id string) string {
	t.Helper()
	input := filepath.Join(p.cfg.Paths.Inbox, id+".png")
	if err := os.WriteFile(input, []byte("original image"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.createJob(t.Context(), id, input, info, nil); err != nil {
		t.Fatal(err)
	}
	return input
}

func runReprocessWork(t *testing.T, p *Processor, work uploadWork) {
	t.Helper()
	if err := p.processCreatedJob(t.Context(), work.jobID, work.uploadPath, work.scanTime, true, work.reprocess, work.state.reporter()); err != nil {
		t.Fatal(err)
	}
	work.state.finish(nil)
}

func TestReprocessRestartsExistingDocumentFromEveryStatus(t *testing.T) {
	for _, status := range []string{StatusReceived, StatusCopyingRaw, StatusProcessing, StatusOCRComplete, StatusClassified, StatusFailed, StatusNeedsReview, StatusArchived, StatusDuplicate} {
		t.Run(status, func(t *testing.T) {
			p := reprocessTestProcessor(t)
			id := "reprocess-1234"
			input := createReprocessTestJob(t, p, id)
			original, _ := p.store.Queries.GetJob(t.Context(), id)
			raw := ""
			if status != StatusReceived {
				raw = filepath.Join(p.cfg.Paths.Raw, "original.png")
				if err := copyFile(input, raw); err != nil {
					t.Fatal(err)
				}
			}
			archive := filepath.Join(p.cfg.Paths.ArchiveRoot, "existing.pdf")
			if err := os.WriteFile(archive, []byte("previous archived PDF"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := p.store.Conn().ExecContext(t.Context(), `UPDATE jobs SET status=?, raw_path=?, file_hash='same-hash', final_path=?, error='old failure', duplicate_of='other-job', manual_override=1, text_hash='old', classification_json='{}' WHERE id=?`, status, raw, archive, id); err != nil {
				t.Fatal(err)
			}
			// An identical record must not divert an explicit reprocess to Duplicates.
			createReprocessTestJob(t, p, "other-job")
			if err := p.store.Queries.SetRawCopy(t.Context(), sqlc.SetRawCopyParams{ID: "other-job", FileHash: "same-hash", Status: StatusArchived, UpdatedAt: db.Now()}); err != nil {
				t.Fatal(err)
			}
			stale := filepath.Join(p.cfg.Paths.Processing, id, "cleaned", "page-0099.png")
			if err := os.MkdirAll(filepath.Dir(stale), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(stale, []byte("old page"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := p.store.SaveEmbedding(t.Context(), id, "old", "model", []string{"old"}, [][]float32{{1, 0}}); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/jobs/"+id+"/retry", nil)
			request.SetPathValue("jobID", id)
			response := httptest.NewRecorder()
			p.handleRetryAPI(response, request)
			if response.Code != http.StatusAccepted {
				t.Fatalf("retry: %d %s", response.Code, response.Body.String())
			}
			var payload map[string]string
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if payload["job_id"] != id || payload["run_id"] == "" {
				t.Fatalf("payload = %v", payload)
			}
			queued, _ := p.store.Queries.GetJob(t.Context(), id)
			if queued.Status != StatusReceived || queued.Error != "" || queued.DuplicateOf != "" || queued.ManualOverride != 0 || queued.TextHash != "" {
				t.Fatalf("stale results: %+v", queued)
			}
			if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("stale page remains: %v", err)
			}
			current, err := p.store.EmbeddingCurrent(t.Context(), id, "old", "model")
			if err != nil || current {
				t.Fatalf("stale embedding: %v %v", current, err)
			}
			if _, err := os.Stat(input); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("superseded inbox input would be ingested again: %v", err)
			}
			work := <-p.uploadQueue
			runReprocessWork(t, p, work)
			job, err := p.store.Queries.GetJob(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			if job.Status != StatusNeedsReview || job.ScanTimestamp != original.ScanTimestamp || job.SourceFilename != original.SourceFilename || (raw != "" && job.RawPath != raw) {
				t.Fatalf("reprocessed job = %+v", job)
			}
			jobs, _ := p.store.Queries.ListAllJobs(t.Context())
			if len(jobs) != 2 {
				t.Fatalf("reprocessing created another record: %d", len(jobs))
			}
			data, err := os.ReadFile(archive)
			if err != nil || string(data) != "previous archived PDF" {
				t.Fatalf("archive changed: %q %v", data, err)
			}
		})
	}
}

func TestReprocessCancelsActiveAttemptBeforeRestart(t *testing.T) {
	p := reprocessTestProcessor(t)
	input := createReprocessTestJob(t, p, "active-1234")
	normalOCR := p.processOCR
	started := make(chan struct{})
	p.processOCR = func(ctx context.Context, _ config.Config, _, _ string, _ progress.Reporter) (ocr.Result, error) {
		close(started)
		<-ctx.Done()
		return ocr.Result{}, ctx.Err()
	}
	done := make(chan error, 1)
	go func() { done <- p.processCreatedJob(t.Context(), "active-1234", input, time.Now(), true, false, nil) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("OCR never started")
	}
	if _, err := p.retryJob(t.Context(), "active-1234"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("old attempt: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("old attempt did not stop")
	}
	p.processOCR = normalOCR
	runReprocessWork(t, p, <-p.uploadQueue)
	job, _ := p.store.Queries.GetJob(t.Context(), "active-1234")
	if job.Status != StatusNeedsReview || job.Error != "" {
		t.Fatalf("old attempt overwrote replacement: %+v", job)
	}
}

func TestReprocessSupersedesQueuedAttempt(t *testing.T) {
	p := reprocessTestProcessor(t)
	input := createReprocessTestJob(t, p, "queued-1234")
	if _, err := p.retryJob(t.Context(), "queued-1234"); err != nil {
		t.Fatal(err)
	}
	if err := p.processCreatedJob(t.Context(), "queued-1234", input, time.Now(), true, false, nil); err == nil {
		t.Fatal("superseded attempt ran")
	}
	runReprocessWork(t, p, <-p.uploadQueue)
}

func TestReprocessRejectsMissingSourceWithoutResettingDocument(t *testing.T) {
	p := reprocessTestProcessor(t)
	input := createReprocessTestJob(t, p, "missing-1234")
	if err := os.Remove(input); err != nil {
		t.Fatal(err)
	}
	before, _ := p.store.Queries.GetJob(t.Context(), "missing-1234")
	if _, err := p.retryJob(t.Context(), "missing-1234"); err == nil {
		t.Fatal("missing source accepted")
	}
	after, _ := p.store.Queries.GetJob(t.Context(), "missing-1234")
	if before != after || len(p.uploadQueue) != 0 {
		t.Fatal("missing source mutated document")
	}
}
