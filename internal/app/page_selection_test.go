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
	"reflect"
	"strings"
	"testing"
)

func TestPageOrderPersistsAndDrivesPreview(t *testing.T) {
	p, cleanup, err := newProcessor(t.Context(), testServerConfig(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	path := filepath.Join(t.TempDir(), "original.pdf")
	if err := os.WriteFile(path, []byte("original PDF"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := p.store.Queries.CreateJob(t.Context(), sqlc.CreateJobParams{ID: "order", Status: StatusNeedsReview, CurrentPath: path, SourceFilename: "original.pdf", ScanTimestamp: db.Now(), UpdatedAt: db.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.store.Conn().Exec(`UPDATE jobs SET page_count=3 WHERE id='order'`); err != nil {
		t.Fatal(err)
	}
	// Echo the requested PDF page sequence so this test also runs without qpdf.
	toolDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(toolDir, "qpdf"), []byte("#!/bin/sh\nfor argument do output=$argument; done\nprintf '%s' \"$4\" > \"$output\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", toolDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, tc := range []struct {
		name, body, preview string
		status              int
		order               []int
	}{
		{"reorder all pages", `{"included":[1,2,3],"order":[3,1,2]}`, "3,1,2", 200, []int{3, 1, 2}},
		{"legacy selection retains order", `{"included":[1,3]}`, "3,1", 200, []int{3, 1, 2}},
		{"missing page", `{"included":[1],"order":[3,1]}`, "3,1", 400, []int{3, 1, 2}},
		{"duplicate order", `{"included":[1],"order":[3,1,1]}`, "3,1", 400, []int{3, 1, 2}},
		{"invalid page", `{"included":[1],"order":[3,1,4]}`, "3,1", 400, []int{3, 1, 2}},
		{"zero page", `{"included":[1],"order":[3,1,0]}`, "3,1", 400, []int{3, 1, 2}},
		{"empty order", `{"included":[1],"order":[]}`, "3,1", 400, []int{3, 1, 2}},
		{"duplicate selection", `{"included":[1,1],"order":[1,2,3]}`, "3,1", 400, []int{3, 1, 2}},
		{"restore excluded page", `{"included":[1,2,3],"order":[3,1,2]}`, "3,1,2", 200, []int{3, 1, 2}},
		{"restore original order", `{"included":[1,2,3],"order":[1,2,3]}`, "original PDF", 200, []int{1, 2, 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
			r.SetPathValue("jobID", "order")
			w := httptest.NewRecorder()
			p.handlePageSelection(w, r)
			if w.Code != tc.status {
				t.Fatalf("save: %d %s", w.Code, w.Body.String())
			}
			r = httptest.NewRequest("GET", "/", nil)
			r.SetPathValue("jobID", "order")
			w = httptest.NewRecorder()
			p.handlePageSelection(w, r)
			var result struct {
				Pages []pageChoice `json:"pages"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			var order []int
			for _, page := range result.Pages {
				order = append(order, page.Page)
			}
			if !reflect.DeepEqual(order, tc.order) {
				t.Fatalf("order: %v, want %v", order, tc.order)
			}
			w = httptest.NewRecorder()
			p.handleSelectedPDF(w, r)
			if w.Code != 200 || w.Body.String() != tc.preview {
				t.Fatalf("preview: %d %q, want %q", w.Code, w.Body.String(), tc.preview)
			}
			assertArchiveContent(t, path, "original PDF")
		})
	}
}

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
		name, body, remote, origin, fetchSite string
		want                                  int
	}{
		{"empty", `{"included":[]}`, "192.168.178.50:1234", "http://192.168.178.115:8844", "", 400},
		{"out of range", `{"included":[4]}`, "192.168.178.50:1234", "http://192.168.178.115:8844", "", 400},
		{"loopback", `{"included":[1,3]}`, "127.0.0.1:1234", "", "", 200},
		{"LAN HTTP origin", `{"included":[1,3]}`, "192.168.178.50:1234", "http://192.168.178.115:8844", "", 200},
		{"LAN fetch metadata", `{"included":[1,3]}`, "192.168.178.50:1234", "", "same-origin", 200},
		{"different origin", `{"included":[2]}`, "192.168.178.50:1234", "http://other.example", "", 403},
		{"different port", `{"included":[2]}`, "192.168.178.50:1234", "http://192.168.178.115:9999", "", 403},
		{"cross-site fetch", `{"included":[2]}`, "192.168.178.50:1234", "", "cross-site", 403},
		{"cross-origin loopback", `{"included":[2]}`, "127.0.0.1:1234", "http://other.example", "", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://192.168.178.115:8844/api/jobs/pages/page-selection", strings.NewReader(tc.body))
			r.RemoteAddr = tc.remote
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			r.SetPathValue("jobID", "pages")
			w := httptest.NewRecorder()
			p.handler().ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("%s: %d %s", tc.body, w.Code, w.Body.String())
			}
		})
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
