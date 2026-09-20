package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"paperless/internal/classify"
	"paperless/internal/config"
	"paperless/internal/db"
	"paperless/internal/db/sqlc"
	"paperless/internal/progress"
)

func TestUploadCorrelatesProgressWithClientBeforeProcessing(t *testing.T) {
	p, cleanup, err := newProcessor(t.Context(), testServerConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	file, err := form.CreateFormFile("document", "scan.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("test upload")); err != nil {
		t.Fatal(err)
	}
	if err := form.WriteField("upload_id", "browser-upload-1"); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/uploads", &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	p.handleUploadAPI(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("upload: %d %s", response.Code, response.Body.String())
	}
	var result map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	snapshots := p.runs.snapshotsSince(make(map[string]int))
	if len(snapshots) != 1 || snapshots[0].RunID != result["run_id"] || snapshots[0].ClientID != "browser-upload-1" || len(snapshots[0].Events) == 0 {
		t.Fatalf("upload progress = %#v", snapshots)
	}
}

func TestParseOCRBoxesUsesWordRows(t *testing.T) {
	boxes := parseOCRBoxes("level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n5\t1\t1\t1\t1\t1\t12\t34\t56\t18\t92.5\tFinanzamt\n4\t1\t1\t1\t1\t0\t10\t30\t100\t25\t-1\tignored\n5\t1\t1\t1\t1\t2\t70\t34\t44\t18\t-1\tlow\n")
	if len(boxes) != 1 {
		t.Fatalf("boxes = %#v", boxes)
	}
	if boxes[0].Text != "Finanzamt" || boxes[0].Left != 12 || boxes[0].Confidence != 92.5 {
		t.Fatalf("box = %#v", boxes[0])
	}
}

func TestWriteSSEUsesTerminalEventNames(t *testing.T) {
	var out strings.Builder
	if err := writeSSE(&out, progress.Event{Phase: "ocr", Step: "render", Percent: 14}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "event: progress") {
		t.Fatalf("progress SSE = %q", out.String())
	}

	out.Reset()
	if err := writeSSE(&out, progress.Event{Level: "info", Done: true, Percent: 100}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "event: done") {
		t.Fatalf("done SSE = %q", out.String())
	}

	out.Reset()
	if err := writeSSE(&out, progress.Event{Level: "error", Done: true, Percent: 92}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "event: failed") {
		t.Fatalf("failed SSE = %q", out.String())
	}
}

func TestJobViewExposesClassificationAndPreviewURLs(t *testing.T) {
	classificationJSON, err := json.Marshal(classify.Classification{
		Sender:            "finanzamt",
		SuggestedFolder:   "Taxes/2026",
		SuggestedFilename: "2026-02-25__finanzamt__tax-letter.pdf",
	})
	if err != nil {
		t.Fatal(err)
	}
	view := jobToView(sqlc.Job{ID: "job-12345678", ClassificationJson: string(classificationJSON)})
	if view.SuggestedPath != "Taxes/2026/2026-02-25__finanzamt__tax-letter.pdf" {
		t.Fatalf("suggested path = %q", view.SuggestedPath)
	}
	if view.URLs.Current != "/files/job-12345678/current" || view.URLs.Pages != "/api/jobs/job-12345678/pages" {
		t.Fatalf("URLs = %#v", view.URLs)
	}
}

func TestDashboardUsesFoldersFromArchiveRoot(t *testing.T) {
	base := t.TempDir()
	cfg := testServerConfig(base)
	for _, folder := range []string{"Finanzen/Steuern", "Familie/Versicherung"} {
		if err := os.MkdirAll(filepath.Join(cfg.Paths.ArchiveRoot, folder), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	processor, cleanup, err := newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	request := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	recorder := httptest.NewRecorder()
	processor.handleDashboardAPI(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var dashboard dashboardResponse
	if err := json.NewDecoder(recorder.Body).Decode(&dashboard); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Familie", "Familie/Versicherung", "Finanzen", "Finanzen/Steuern"} {
		if !containsString(dashboard.Folders, want) {
			t.Fatalf("folders %v missing %q", dashboard.Folders, want)
		}
	}
	if dashboard.Settings.ArchiveRoot != cfg.Paths.ArchiveRoot || !dashboard.Settings.ArchiveExists {
		t.Fatalf("settings = %#v", dashboard.Settings)
	}
}

func TestDashboardDropsRemovedArchiveFolders(t *testing.T) {
	base := t.TempDir()
	cfg := testServerConfig(base)
	removed := filepath.Join(cfg.Paths.ArchiveRoot, "Old", "Folder")
	if err := os.MkdirAll(removed, 0o755); err != nil {
		t.Fatal(err)
	}
	processor, cleanup, err := newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	request := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	recorder := httptest.NewRecorder()
	processor.handleDashboardAPI(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("initial status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if err := os.RemoveAll(filepath.Join(cfg.Paths.ArchiveRoot, "Old")); err != nil {
		t.Fatal(err)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	recorder = httptest.NewRecorder()
	processor.handleDashboardAPI(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("refreshed status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var dashboard dashboardResponse
	if err := json.NewDecoder(recorder.Body).Decode(&dashboard); err != nil {
		t.Fatal(err)
	}
	if containsString(dashboard.Folders, "Old") || containsString(dashboard.Folders, "Old/Folder") {
		t.Fatalf("removed archive folders still present: %v", dashboard.Folders)
	}
}

func TestDashboardReportsFMWithEmptyArchive(t *testing.T) {
	cfg := testServerConfig(t.TempDir())
	cfg.Policy.KnownFolders = nil
	cfg.LLM.Provider = "fm"
	cfg.LLM.Model = "system"
	cfg.LLM.Enabled = true
	processor, cleanup, err := newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	recorder := httptest.NewRecorder()
	processor.handleDashboardAPI(recorder, httptest.NewRequest(http.MethodGet, "/api/dashboard", nil))
	var dashboard dashboardResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &dashboard); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || dashboard.Folders == nil || dashboard.Settings.ModelProvider != "fm" || dashboard.Settings.Model != "system" || !dashboard.Settings.ModelEnabled {
		t.Fatalf("unexpected dashboard: %s", recorder.Body.String())
	}
}

func TestConfiguredFoldersAreAvailableBeforeFirstApproval(t *testing.T) {
	base := t.TempDir()
	cfg := testServerConfig(base)
	cfg.Policy.KnownFolders = []string{"Belege"}
	processor, cleanup, err := newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	folders, err := processor.folders(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(folders, "Belege") {
		t.Fatalf("configured folder missing before approval: %v", folders)
	}
}

func TestFinalPathStaysInsideArchiveRoot(t *testing.T) {
	base := t.TempDir()
	cfg := testServerConfig(base)
	processor := &Processor{cfg: cfg}
	if _, err := processor.finalPath("../outside", "scan.pdf"); err == nil {
		t.Fatal("expected traversal folder to be rejected")
	}
	if _, err := processor.finalPath(filepath.Join(base, "outside"), "scan.pdf"); err == nil {
		t.Fatal("expected absolute folder to be rejected")
	}
	path, err := processor.finalPath("Taxes/2026", "letter.pdf")
	if err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(cfg.Paths.ArchiveRoot, "Taxes", "2026")
	if filepath.Dir(path) != wantDir {
		t.Fatalf("path = %q, want dir %q", path, wantDir)
	}
}

func TestApprovePersistsCorrectedDocumentType(t *testing.T) {
	base := t.TempDir()
	cfg := testServerConfig(base)
	processor, cleanup, err := newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	jobID := "job-receipt-review"
	currentPath := filepath.Join(cfg.Paths.Review, "receipt.pdf")
	if err := os.WriteFile(currentPath, []byte("searchable PDF"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := db.Now()
	if err := processor.store.Queries.CreateJob(t.Context(), sqlc.CreateJobParams{
		ID: jobID, SourceFilename: "scan.pdf", CurrentPath: currentPath,
		ScanTimestamp: now, UpdatedAt: now, Status: StatusNeedsReview,
	}); err != nil {
		t.Fatal(err)
	}
	classificationJSON, _ := json.Marshal(classify.Classification{DocumentType: "tax-letter", Sender: "total-tankstelle"})
	if err := processor.store.Queries.SetClassified(t.Context(), sqlc.SetClassifiedParams{
		ClassificationJson: string(classificationJSON), Confidence: .8, Summary: "Fuel receipt",
		PhysicalOriginalAction: "keep_original", Status: StatusNeedsReview, UpdatedAt: now, ID: jobID,
	}); err != nil {
		t.Fatal(err)
	}

	finalPath, err := processor.ApproveJob(t.Context(), jobID, "Belege", "2025-06-07__total-tankstelle__receipt.pdf", "receipt", "discard_candidate")
	if err != nil {
		t.Fatal(err)
	}
	job, err := processor.store.Queries.GetJob(t.Context(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	var corrected classify.Classification
	if err := json.Unmarshal([]byte(job.ClassificationJson), &corrected); err != nil {
		t.Fatal(err)
	}
	if corrected.DocumentType != "receipt" || corrected.SuggestedFolder != "Belege" || job.Status != StatusArchived {
		t.Fatalf("job = %#v, classification = %#v", job, corrected)
	}
	if finalPath != job.FinalPath {
		t.Fatalf("final path = %q, stored = %q", finalPath, job.FinalPath)
	}
}

func TestRejectPermanentlyDeletesJobAndArtifacts(t *testing.T) {
	base := t.TempDir()
	cfg := testServerConfig(base)
	processor, cleanup, err := newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	jobID := "0123456789abcdef0123456789abcdef"
	rawPath := filepath.Join(cfg.Paths.Raw, "20260830-120000__01234567__scan.pdf")
	processingInput := filepath.Join(cfg.Paths.Processing, filepath.Base(rawPath))
	workDir := filepath.Join(cfg.Paths.Processing, jobID)
	textPath := filepath.Join(workDir, "ocr.txt")
	pagePath := filepath.Join(workDir, "cleaned", "page-0001.png")
	currentPath := filepath.Join(cfg.Paths.Review, "01234567__scan.pdf")
	for path, contents := range map[string]string{
		rawPath:         "raw",
		processingInput: "processing input",
		textPath:        "ocr text",
		pagePath:        "cleaned page",
		currentPath:     "review pdf",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	now := db.Now()
	if err := processor.store.Queries.CreateJob(t.Context(), sqlc.CreateJobParams{
		ID: jobID, SourceFilename: "scan.pdf", CurrentPath: processingInput,
		ScanTimestamp: now, UpdatedAt: now, Status: StatusReceived,
	}); err != nil {
		t.Fatal(err)
	}
	if err := processor.store.Queries.SetRawCopy(t.Context(), sqlc.SetRawCopyParams{
		RawPath: rawPath, FileHash: "hash", Status: StatusCopyingRaw, UpdatedAt: now, ID: jobID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := processor.store.Queries.SetOCRComplete(t.Context(), sqlc.SetOCRCompleteParams{
		CurrentPath: currentPath, TextPath: textPath, TextHash: "text-hash", PageCount: 1,
		InputKind: "scan", TextSource: "ocr", Status: StatusOCRComplete, UpdatedAt: now, ID: jobID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := processor.store.Queries.SetNeedsReview(t.Context(), sqlc.SetNeedsReviewParams{
		CurrentPath: currentPath, Status: StatusNeedsReview, UpdatedAt: now, ID: jobID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := processor.store.Queries.AddEvent(t.Context(), sqlc.AddEventParams{
		JobID: jobID, CreatedAt: now, Level: "info", Message: "needs review",
	}); err != nil {
		t.Fatal(err)
	}

	if err := processor.RejectJob(t.Context(), jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := processor.store.Queries.GetJob(t.Context(), jobID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("job still exists or lookup failed unexpectedly: %v", err)
	}
	for _, path := range []string{rawPath, processingInput, textPath, pagePath, currentPath, workDir} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("artifact still exists at %s: %v", path, err)
		}
	}
	var eventCount int
	if err := processor.store.Conn().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM events WHERE job_id = ?", jobID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 0 {
		t.Fatalf("events still exist for deleted job: %d", eventCount)
	}
	reviews, err := processor.store.Queries.ListReviewJobs(t.Context(), 10)
	if err != nil || len(reviews) != 0 {
		t.Fatalf("review queue = %#v, err = %v", reviews, err)
	}
}

func TestRejectRefusesToDeleteFilesOutsideRuntimeFolders(t *testing.T) {
	base := t.TempDir()
	cfg := testServerConfig(filepath.Join(base, "runtime"))
	processor, cleanup, err := newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	outsidePath := filepath.Join(base, "keep.pdf")
	if err := os.WriteFile(outsidePath, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	jobID := "abcdef0123456789abcdef0123456789"
	now := db.Now()
	if err := processor.store.Queries.CreateJob(t.Context(), sqlc.CreateJobParams{
		ID: jobID, SourceFilename: "keep.pdf", CurrentPath: outsidePath,
		ScanTimestamp: now, UpdatedAt: now, Status: StatusNeedsReview,
	}); err != nil {
		t.Fatal(err)
	}

	if err := processor.RejectJob(t.Context(), jobID); err == nil {
		t.Fatal("expected unsafe artifact path to be rejected")
	}
	if _, err := os.Stat(outsidePath); err != nil {
		t.Fatalf("outside file was removed: %v", err)
	}
	if _, err := processor.store.Queries.GetJob(t.Context(), jobID); err != nil {
		t.Fatalf("job should remain after rejected deletion: %v", err)
	}
}

func TestCandidateFoldersPrioritizesOCRMatches(t *testing.T) {
	folders := []string{"Auto", "Gesundheit", "Finanzamt", "Finanzamt/Steuererklaerung", "Versicherung", "Wohnung"}
	got := candidateFolders("Schreiben vom Finanzamt zur Steuererklaerung", folders, 3)
	if !containsString(got, "Finanzamt") || !containsString(got, "Finanzamt/Steuererklaerung") {
		t.Fatalf("candidate folders = %v", got)
	}
}

func TestCandidateFoldersKeepsReceiptDestinationAheadOfTaxTerms(t *testing.T) {
	folders := []string{"09 Rechnungen und Belege/Belege", "Finanzamt/Steuererklaerung/2025"}
	for index := 0; index < 30; index++ {
		folders = append(folders, fmt.Sprintf("Archive/Folder-%02d", index))
	}
	got := candidateFolders("KUNDENBELEG Gesamtbetrag 15,17 EUR MwSt Steuernummer VISA", folders, 8)
	if !containsString(got, "09 Rechnungen und Belege/Belege") {
		t.Fatalf("receipt destination missing from %v", got)
	}
}

func TestEmbeddedReactAppIsServed(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	recorder := httptest.NewRecorder()
	handleWebAsset(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `<div id="root"></div>`) {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func testServerConfig(base string) config.Config {
	cfg := config.Default()
	cfg.Paths.Inbox = filepath.Join(base, "inbox")
	cfg.Paths.Raw = filepath.Join(base, "raw")
	cfg.Paths.Processing = filepath.Join(base, "processing")
	cfg.Paths.Archive = filepath.Join(base, "archive")
	cfg.Paths.Review = filepath.Join(base, "review")
	cfg.Paths.Rejected = filepath.Join(base, "rejected")
	cfg.Paths.Duplicates = filepath.Join(base, "duplicates")
	cfg.Paths.Logs = filepath.Join(base, "logs")
	cfg.Paths.StateDir = filepath.Join(base, "state")
	cfg.Paths.ArchiveRoot = filepath.Join(base, "dropbox")
	cfg.Policy.KnownFolders = nil
	cfg.SenderFolders = nil
	for _, dir := range append(cfg.RuntimeDirs(), cfg.Paths.ArchiveRoot) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			panic(err)
		}
	}
	return cfg
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
