package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"paperless/internal/classify"
	"paperless/internal/config"
	"paperless/internal/db"
	"paperless/internal/db/sqlc"
)

func TestReviewApprovalTeachesRecipientScopedRoutingAfterRestart(t *testing.T) {
	cfg := testServerConfig(t.TempDir())
	cfg.LLM.Enabled = false
	p, closeStore, err := newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(cfg.Paths.Review, "review.pdf")
	if err := os.WriteFile(filename, []byte("test document"), 0600); err != nil {
		t.Fatal(err)
	}
	now := db.Now()
	id := "recipient-review"
	if err := p.store.Queries.CreateJob(t.Context(), sqlc.CreateJobParams{ID: id, SourceFilename: "review.pdf", CurrentPath: filename, Status: StatusNeedsReview, ScanTimestamp: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	original, _ := json.Marshal(classify.Classification{Sender: "merchant", Recipient: "wrong-recipient", RecipientScope: "gbr"})
	if err := p.store.Queries.SetClassified(t.Context(), sqlc.SetClassifiedParams{ID: id, ClassificationJson: string(original), Status: StatusNeedsReview, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/jobs/recipient-review/approve", strings.NewReader(`{"folder":"Misc/Alpha","filename":"invoice.pdf","document_type":"routine-invoice","physical_original_action":"keep_original","recipient":"Alex Example","recipient_scope":"personal"}`))
	request.SetPathValue("jobID", id)
	response := httptest.NewRecorder()
	p.handleApproveAPI(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("approve %d: %s", response.Code, response.Body.String())
	}
	job, err := p.store.Queries.GetJob(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	var c classify.Classification
	json.Unmarshal([]byte(job.ClassificationJson), &c)
	if c.Recipient != "alex-example" || c.RecipientScope != "personal" || job.Status != StatusArchived {
		t.Fatalf("approval not persisted: %+v", c)
	}
	closeStore()
	p, closeStore, err = newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer closeStore()
	profiles, err := p.store.RecipientProfiles(t.Context())
	if err != nil || len(profiles) != 1 || profiles[0].Scope != "personal" {
		t.Fatalf("profiles=%+v error=%v", profiles, err)
	}
	folders := []string{"Misc/Alpha", "Business"}
	for i := 0; i < 30; i++ {
		folders = append(folders, strings.Repeat("Z", i+1))
	}
	c = p.classifyDocument(t.Context(), "Merchant\nHerrn Alex Example\nMusterweg 1\n12345 Berlin\nRechnung 12.03.2026", "scan.pdf", time.Now(), folders, nil)
	if c.SuggestedFolder != "Misc/Alpha" {
		t.Fatalf("learned destination omitted after restart: %+v", c)
	}
	if _, err := p.ApproveJob(t.Context(), id, "Misc/Alpha", "invoice.pdf", "routine-invoice", "keep_original"); err == nil {
		t.Fatal("a second approval must not count as another learning example")
	}
}

func TestRecipientProfileAPIValidatesCapacityAndFolder(t *testing.T) {
	cfg := testServerConfig(t.TempDir())
	p, cleanup, err := newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for _, body := range []string{`{"name":"Example","scope":"invented"}`, `{"name":"Example","scope":"personal","folder_prefix":"../outside"}`} {
		response := httptest.NewRecorder()
		p.handleSaveRecipientAPI(response, httptest.NewRequest(http.MethodPost, "/api/recipients", strings.NewReader(body)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid profile accepted: %s", body)
		}
	}
	response := httptest.NewRecorder()
	p.handleSaveRecipientAPI(response, httptest.NewRequest(http.MethodPost, "/api/recipients", strings.NewReader(`{"name":"Example & Partner GbR","scope":"gbr","aliases":["Example and Partner GbR"],"folder_prefix":"Business"}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("save profile: %s", response.Body.String())
	}
}

func TestSavedRecipientOverrideLearnsAliasOnlyAfterSuccessfulApproval(t *testing.T) {
	cfg := testServerConfig(t.TempDir())
	p, cleanup, err := newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if err := p.store.SaveRecipientProfile(t.Context(), config.RecipientProfile{Name: "Alex Example", Scope: "personal"}); err != nil {
		t.Fatal(err)
	}
	profiles, _ := p.store.RecipientProfiles(t.Context())
	id := profiles[0].ID
	file := filepath.Join(cfg.Paths.Review, "alias.pdf")
	os.WriteFile(file, []byte("document"), 0600)
	now := db.Now()
	p.store.Queries.CreateJob(t.Context(), sqlc.CreateJobParams{ID: "alias-review", SourceFilename: "alias.pdf", CurrentPath: file, Status: StatusNeedsReview, ScanTimestamp: now, UpdatedAt: now})
	original, _ := json.Marshal(classify.Classification{Recipient: "alex-examp1e", RecipientScope: "personal", DocumentType: "receipt"})
	p.store.Queries.SetClassified(t.Context(), sqlc.SetClassifiedParams{ID: "alias-review", ClassificationJson: string(original), Status: StatusNeedsReview, UpdatedAt: now})
	for _, body := range []string{
		fmt.Sprintf(`{"folder":"Example GbR","filename":"letter.pdf","document_type":"contract","recipient_profile_id":%d}`, id),
		`{"folder":"Personal","filename":"letter.pdf","document_type":"contract","recipient_profile_id":9999}`,
		fmt.Sprintf(`{"folder":"Personal","filename":"letter.pdf","document_type":"contract","recipient_profile_id":%d,"recipient":"Spoofed","recipient_scope":"gbr"}`, id),
	} {
		request := httptest.NewRequest(http.MethodPost, "/approve", strings.NewReader(body))
		request.SetPathValue("jobID", "alias-review")
		response := httptest.NewRecorder()
		p.handleApproveAPI(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid approval accepted: %s", response.Body.String())
		}
		profiles, _ = p.store.RecipientProfiles(t.Context())
		if len(profiles[0].Aliases) != 0 {
			t.Fatal("failed review learned an alias")
		}
		job, _ := p.store.Queries.GetJob(t.Context(), "alias-review")
		if job.Status != StatusNeedsReview {
			t.Fatal("failed review archived document")
		}
	}
	body := fmt.Sprintf(`{"folder":"Personal","filename":"letter.pdf","document_type":"contract","physical_original_action":"discard_candidate","recipient_profile_id":%d}`, id)
	request := httptest.NewRequest(http.MethodPost, "/approve", strings.NewReader(body))
	request.SetPathValue("jobID", "alias-review")
	response := httptest.NewRecorder()
	p.handleApproveAPI(response, request)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body.String())
	}
	profiles, _ = p.store.RecipientProfiles(t.Context())
	if len(profiles) != 1 || len(profiles[0].Aliases) != 1 || profiles[0].Aliases[0] != "alex-examp1e" {
		t.Fatalf("aliases not learned: %+v", profiles)
	}
	job, _ := p.store.Queries.GetJob(t.Context(), "alias-review")
	var c classify.Classification
	json.Unmarshal([]byte(job.ClassificationJson), &c)
	if c.Recipient != "alex-example" || c.RecipientProfileID != id || c.DetectedRecipient != "alex-examp1e" || job.PhysicalOriginalAction != "keep_original" {
		t.Fatalf("saved identity or recommendation wrong: %+v", c)
	}
	cfg.LLM.Enabled = false
	cfg.RecipientProfiles = profiles
	next := classify.Classify(t.Context(), cfg, "Merchant\nHerrn Alex Examp1e\nMusterweg 1\n12345 Berlin\nRechnung", "next.pdf", time.Now(), []string{"Personal"})
	if next.Recipient != "alex-example" || next.RecipientProfileID != id || next.RecipientScope != "personal" {
		t.Fatalf("learned spelling not preferred: %+v", next)
	}
}

func TestJobLayoutIncludesExistingOCRAndMissingTextReturns404(t *testing.T) {
	cfg := testServerConfig(t.TempDir())
	p, cleanup, err := newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	now := db.Now()
	p.store.Queries.CreateJob(t.Context(), sqlc.CreateJobParams{ID: "layout-job", SourceFilename: "letter.pdf", CurrentPath: "letter.pdf", Status: StatusNeedsReview, ScanTimestamp: now, UpdatedAt: now})
	request := httptest.NewRequest(http.MethodGet, "/layout", nil)
	request.SetPathValue("jobID", "layout-job")
	response := httptest.NewRecorder()
	p.handleJobLayoutAPI(response, request)
	if response.Code != 404 {
		t.Fatalf("missing text: %d", response.Code)
	}
	work := filepath.Join(cfg.Paths.Processing, "layout-job")
	os.MkdirAll(filepath.Join(work, "ocr"), 0700)
	path := filepath.Join(work, "ocr.txt")
	os.WriteFile(path, []byte("Original raw text"), 0600)
	os.WriteFile(filepath.Join(work, "ocr", "page-0001.tsv"), []byte("5\t1\t1\t1\t1\t1\t10\t20\t100\t15\t90\tRecognized\n5\t1\t1\t1\t1\t2\t115\t20\t50\t15\t90\twords"), 0600)
	_, err = p.store.Conn().ExecContext(t.Context(), `UPDATE jobs SET text_path=?,page_count=1 WHERE id='layout-job'`, path)
	if err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	p.handleJobLayoutAPI(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "Recognized words") || !strings.Contains(response.Body.String(), `"markdown"`) {
		t.Fatalf("layout response: %s", response.Body.String())
	}
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "Original raw text" {
		t.Fatal("layout changed raw OCR")
	}
}
