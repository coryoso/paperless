package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"paperless/internal/config"
	"paperless/internal/db/sqlc"
	"paperless/internal/ocr"
	"paperless/internal/progress"
)

// Run the real pipeline in another process, replacing only the expensive OCR
// engine. Every notification must arrive after its corresponding saved stage.
func TestDashboardPipelineProcess(t *testing.T) {
	cfgPath := os.Getenv("PAPERLESS_TEST_PIPELINE_CONFIG")
	if cfgPath == "" {
		return
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	p, cleanup, err := newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	p.processOCR = func(_ context.Context, _ config.Config, input, workDir string, _ progress.Reporter) (ocr.Result, error) {
		content, err := os.ReadFile(input)
		if err != nil {
			return ocr.Result{}, err
		}
		if string(content) == "fail" {
			return ocr.Result{}, errors.New("test OCR failure")
		}
		if err := os.MkdirAll(workDir, 0700); err != nil {
			return ocr.Result{}, err
		}
		pdf, text := filepath.Join(workDir, "searchable.pdf"), filepath.Join(workDir, "text.txt")
		if err := os.WriteFile(pdf, []byte("searchable PDF"), 0600); err != nil {
			return ocr.Result{}, err
		}
		if err := os.WriteFile(text, []byte("Merchant receipt"), 0600); err != nil {
			return ocr.Result{}, err
		}
		return ocr.Result{SearchablePDF: pdf, TextPath: text, Text: "Merchant receipt", PageCount: 1}, nil
	}
	process := func(id, content string) error {
		input := filepath.Join(cfg.Paths.Inbox, id+".pdf")
		if err := os.WriteFile(input, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := p.ProcessUploadedFile(t.Context(), id, input, nil)
		return err
	}
	if err := process("pipeline-one", "original"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ApproveJob(t.Context(), "pipeline-one", "Letters", "receipt.pdf", "receipt", ""); err != nil {
		t.Fatal(err)
	}
	if err := process("pipeline-duplicate", "original"); err != nil {
		t.Fatal(err)
	}
	if err := process("pipeline-failed", "fail"); err == nil {
		t.Fatal("expected OCR failure")
	}
	if err := p.RejectJob(t.Context(), "pipeline-failed"); err != nil {
		t.Fatal(err)
	}
}

func TestPipelineNotificationsAcrossProcesses(t *testing.T) {
	cfg := testServerConfig(t.TempDir())
	cfg.LLM.Enabled = false
	service, cleanup, err := newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var mu sync.Mutex
	histories := map[string][]string{}
	record := func(w http.ResponseWriter, r *http.Request) {
		service.handleDashboardNotification(w, r)
		// The CLI waits for this request to finish before saving its next stage.
		jobs, err := service.store.Queries.ListAllJobs(r.Context())
		if err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		for _, job := range jobs {
			history := histories[job.ID]
			if len(history) == 0 || history[len(history)-1] != job.Status {
				histories[job.ID] = append(history, job.Status)
			}
		}
		found := false
		for _, job := range jobs {
			if job.ID == "pipeline-failed" {
				found = true
			}
		}
		if !found && len(histories["pipeline-failed"]) > 0 {
			histories["pipeline-failed"] = append(histories["pipeline-failed"], "deleted")
		}
	}
	server := httptest.NewServer(http.HandlerFunc(record))
	defer server.Close()
	stop, err := service.startDashboardRelay(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	info, err := os.Stat(service.dashboardRelayPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("discovery permissions: %o", info.Mode().Perm())
	}
	data, _ := json.Marshal(cfg)
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestDashboardPipelineProcess$")
	command.Env = append(os.Environ(), "PAPERLESS_TEST_PIPELINE_CONFIG="+cfgPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("pipeline: %v\n%s", err, output)
	}
	mu.Lock()
	defer mu.Unlock()
	expected := map[string][]string{
		"pipeline-one":       {StatusReceived, StatusCopyingRaw, StatusProcessing, StatusOCRComplete, StatusClassified, StatusNeedsReview, StatusArchived},
		"pipeline-duplicate": {StatusReceived, StatusCopyingRaw, StatusDuplicate},
		"pipeline-failed":    {StatusReceived, StatusCopyingRaw, StatusProcessing, StatusFailed, "deleted"},
	}
	if !reflect.DeepEqual(histories, expected) {
		t.Fatalf("notifications after persisted transitions:\ngot  %#v\nwant %#v", histories, expected)
	}
}

func TestDashboardNotificationsAreExplicitAndAuthenticated(t *testing.T) {
	cfg := testServerConfig(t.TempDir())
	p, cleanup, err := newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	events, unsubscribe := p.dashboardEvents.subscribe()
	defer unsubscribe()
	<-events
	// Raw database writes do not publish. Only the pipeline/application owns events.
	if err := p.store.Queries.CreateJob(t.Context(), sqlc.CreateJobParams{ID: "raw", SourceFilename: "raw.pdf", Status: StatusReceived}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-events:
		t.Fatal("database write emitted an event")
	default:
	}
	info, err := os.Stat(cfg.DBPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.createJob(t.Context(), "raw", "raw.pdf", info, nil); err == nil {
		t.Fatal("expected duplicate database insert failure")
	}
	select {
	case <-events:
		t.Fatal("failed persistence emitted an event")
	default:
	}
	p.dashboardRelayToken = "secret"
	for _, token := range []string{"", "Bearer wrong"} {
		request := httptest.NewRequest(http.MethodPost, "/api/internal/dashboard/notify", nil)
		request.Header.Set("Authorization", token)
		writer := httptest.NewRecorder()
		p.handleDashboardNotification(writer, request)
		if writer.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated request: %d", writer.Code)
		}
		select {
		case <-events:
			t.Fatal("unauthorized relay emitted an event")
		default:
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/api/internal/dashboard/notify", nil)
	request.Header.Set("Authorization", "Bearer secret")
	writer := httptest.NewRecorder()
	p.handleDashboardNotification(writer, request)
	if writer.Code != http.StatusNoContent {
		t.Fatalf("authorized request: %d", writer.Code)
	}
	select {
	case <-events:
	default:
		t.Fatal("authorized notification was lost")
	}
	var triggers int
	if err := p.store.Conn().QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_master WHERE type='trigger'").Scan(&triggers); err != nil {
		t.Fatal(err)
	}
	if triggers != 0 {
		t.Fatal("unexpected database triggers")
	}
}
