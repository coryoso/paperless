package app

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"paperless/internal/classify"
	"paperless/internal/db"
	"paperless/internal/db/sqlc"
	"paperless/internal/document"
	"path/filepath"
	"strings"
	"testing"
)

func TestPageSelectionPersistsAndRejectsInvalidCuts(t *testing.T) {
	p, cleanup, err := newProcessor(t.Context(), testServerConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	err = p.store.Queries.CreateJob(t.Context(), sqlc.CreateJobParams{ID: "pages", Status: StatusNeedsReview, SourceFilename: "test.pdf", ScanTimestamp: db.Now(), UpdatedAt: db.Now()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.store.Conn().Exec(`UPDATE jobs SET page_count=3 WHERE id='pages'`)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		body string
		want int
	}{{`{"included":[]}`, 400}, {`{"included":[4]}`, 400}, {`{"included":[1,3]}`, 200}} {
		r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.SetPathValue("jobID", "pages")
		w := httptest.NewRecorder()
		p.handlePageSelection(w, r)
		if w.Code != tc.want {
			t.Fatalf("%s: %d %s", tc.body, w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.SetPathValue("jobID", "pages")
	w := httptest.NewRecorder()
	p.handlePageSelection(w, r)
	var result struct {
		Pages []pageChoice `json:"pages"`
	}
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Pages) != 3 || !result.Pages[1].Excluded || result.Pages[0].Excluded || result.Pages[2].Excluded {
		t.Fatal(result)
	}
	_, err = p.store.Conn().Exec(`UPDATE jobs SET status='archived' WHERE id='pages'`)
	if err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("POST", "/", strings.NewReader(`{"included":[1]}`))
	r.RemoteAddr = "127.0.0.1:1234"
	r.SetPathValue("jobID", "pages")
	w = httptest.NewRecorder()
	p.handlePageSelection(w, r)
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
}

func TestExplicitRecipientPostalFieldsSurviveApproval(t *testing.T) {
	cfg := testServerConfig(t.TempDir())
	p, cleanup, err := newProcessor(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	path := filepath.Join(cfg.Paths.Review, "sample.pdf")
	if err = os.WriteFile(path, []byte("test PDF"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = p.store.Queries.CreateJob(t.Context(), sqlc.CreateJobParams{ID: "postal", Status: StatusNeedsReview, CurrentPath: path, SourceFilename: "sample.pdf", ScanTimestamp: db.Now(), UpdatedAt: db.Now()}); err != nil {
		t.Fatal(err)
	}
	_, err = p.ApproveJob(t.Context(), "postal", "Letters", "sample.pdf", "", "", RecipientCorrection{Recipient: "Alex Example", Scope: "personal", PostalAddress: &document.PostalAddress{StreetName: "Example Road", HouseNumber: "12a", PostalCode: "SW1A 1AA", City: "London"}})
	if err != nil {
		t.Fatal(err)
	}
	job, err := p.store.Queries.GetJob(t.Context(), "postal")
	if err != nil {
		t.Fatal(err)
	}
	var c classify.Classification
	if err = json.Unmarshal([]byte(job.ClassificationJson), &c); err != nil {
		t.Fatal(err)
	}
	a := c.Metadata.Recipient.Addresses[0]
	if a.PostalCode != "SW1A 1AA" || a.City != "London" || a.StreetName != "Example Road" || a.HouseNumber != "12a" {
		t.Fatal(a)
	}
}
