package app

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"paperless/internal/config"
	"paperless/internal/ocr"
)

func TestDryRunLocalAcceptancePDF(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping local PDF acceptance test in short mode")
	}
	requireTool(t, "pdftoppm")
	requireTool(t, "qpdf")
	requireTool(t, "tesseract")
	available, err := ocr.AvailableLanguages(t.Context())
	if err != nil {
		t.Skipf("cannot list Tesseract languages: %v", err)
	}
	if !available["eng"] {
		t.Skip("Tesseract eng language data is not installed")
	}

	pdfPath := localAcceptancePDFPath(t)
	cfg := acceptanceConfig(t)
	result, err := DryRunFile(t.Context(), cfg, pdfPath)
	if err != nil {
		t.Fatal(err)
	}

	if result.OCR.PageCount != 2 {
		t.Fatalf("page count = %d, want 2", result.OCR.PageCount)
	}
	if len(result.OCR.Pages) != 2 {
		t.Fatalf("page results = %d, want 2", len(result.OCR.Pages))
	}
	if _, err := os.Stat(result.OCRPDFPath); err != nil {
		t.Fatalf("searchable PDF missing: %v", err)
	}
	if _, err := os.Stat(result.TextPath); err != nil {
		t.Fatalf("OCR text missing: %v", err)
	}
	for _, page := range result.OCR.Pages {
		if _, err := os.Stat(page.CleanedImage); err != nil {
			t.Fatalf("cleaned image missing: %v", err)
		}
		if _, err := os.Stat(page.SearchablePDF); err != nil {
			t.Fatalf("page OCR PDF missing: %v", err)
		}
	}
	deskewed := false
	for index, page := range result.OCR.Pages {
		if page.Cropped {
			t.Fatalf("page %d was cropped; this acceptance scan should only need straightening", index+1)
		}
		if math.Abs(page.DeskewAngle) >= 0.15 {
			deskewed = true
		}
	}
	if !deskewed {
		t.Fatal("expected at least one page to be deskewed")
	}
	for _, want := range []string{"Finanzamt", "Steuernummer"} {
		if !strings.Contains(result.OCRText, want) {
			t.Fatalf("OCR text does not contain %q", want)
		}
	}
	if result.Classification.DocumentDate != "2026-02-25" {
		t.Fatalf("document date = %q", result.Classification.DocumentDate)
	}
	if result.Classification.DocumentType != "tax-letter" {
		t.Fatalf("document type = %q", result.Classification.DocumentType)
	}
	if result.Classification.SuggestedFolder != "02 Finanzen und Steuern/Finanzamt" {
		t.Fatalf("folder = %q", result.Classification.SuggestedFolder)
	}
	wantSuggested := filepath.Join("02 Finanzen und Steuern/Finanzamt", "2026-02-25__finanzamt__tax-letter__tax-letter.pdf")
	if result.SuggestedPath != wantSuggested {
		t.Fatalf("suggested path = %q, want %q", result.SuggestedPath, wantSuggested)
	}
	if result.WouldAutoFile {
		t.Fatal("local acceptance PDF should stay in dry-run/review, not auto-file")
	}
	if result.Classification.PhysicalOriginalAction != "keep_original" {
		t.Fatalf("paper action = %q", result.Classification.PhysicalOriginalAction)
	}
}

func TestUploadedDocumentEntersNormalReviewFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping local PDF acceptance test in short mode")
	}
	requireTool(t, "pdftoppm")
	requireTool(t, "qpdf")
	requireTool(t, "tesseract")
	available, err := ocr.AvailableLanguages(t.Context())
	if err != nil || !available["eng"] {
		t.Skip("Tesseract English language data is unavailable")
	}

	cfg := acceptanceConfig(t)
	processor, cleanup, err := newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	uploadPath := filepath.Join(cfg.Paths.Processing, "uploads", "manual-scan.pdf")
	if err := copyFile(localAcceptancePDFPath(t), uploadPath); err != nil {
		t.Fatal(err)
	}
	jobID, err := processor.ProcessUploadedFile(t.Context(), "manual-job-1234", uploadPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	job, err := processor.store.Queries.GetJob(t.Context(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != StatusNeedsReview {
		t.Fatalf("status = %q, want %q", job.Status, StatusNeedsReview)
	}
	if job.RawPath == "" || job.CurrentPath == "" || job.PageCount != 2 {
		t.Fatalf("job did not retain normal pipeline artifacts: %#v", job)
	}
	if job.InputKind != "scan" || job.TextSource != "ocr" {
		t.Fatalf("input metadata = %q/%q, want scan/ocr", job.InputKind, job.TextSource)
	}
	if _, err := os.Stat(job.RawPath); err != nil {
		t.Fatalf("raw copy missing: %v", err)
	}
	if _, err := os.Stat(job.CurrentPath); err != nil {
		t.Fatalf("review PDF missing: %v", err)
	}
	reviews, err := processor.store.Queries.ListReviewJobs(t.Context(), 10)
	if err != nil || len(reviews) != 1 || reviews[0].ID != jobID {
		t.Fatalf("review jobs = %#v, err = %v", reviews, err)
	}
	all, err := processor.store.Queries.ListAllJobs(t.Context())
	if err != nil || len(all) != 1 || all[0].ID != jobID {
		t.Fatalf("all jobs = %#v, err = %v", all, err)
	}
}

func localAcceptancePDFPath(t *testing.T) string {
	t.Helper()
	path := os.Getenv("PAPERLESS_ACCEPTANCE_PDF")
	if path == "" {
		t.Skip("set PAPERLESS_ACCEPTANCE_PDF to run the local acceptance test")
	}
	if _, err := os.Stat(path); err != nil {
		t.Skipf("local acceptance PDF not available: %s", path)
	}
	return path
}

func acceptanceConfig(t *testing.T) config.Config {
	t.Helper()
	base := t.TempDir()
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
	cfg.Paths.ArchiveRoot = filepath.Join(base, "archive-root")
	taxFolder := filepath.Join("02 Finanzen und Steuern", "Finanzamt")
	cfg.Policy.KnownFolders = []string{taxFolder}
	cfg.SenderFolders = map[string]string{"finanzamt": taxFolder}
	if err := os.MkdirAll(filepath.Join(cfg.Paths.ArchiveRoot, taxFolder), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg.LLM.Enabled = false
	return cfg
}

func requireTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s is not installed", name)
	}
}
