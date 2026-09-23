package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"paperless/internal/db"
	"paperless/internal/db/sqlc"
)

func archivedReviewFixture(t *testing.T) (*Processor, string, string, string) {
	t.Helper()
	p := reprocessTestProcessor(t)
	id := "replace-review-1234"
	source := createReprocessTestJob(t, p, id)
	old := filepath.Join(p.cfg.Paths.ArchiveRoot, "Letters", "old.pdf")
	if err := os.MkdirAll(filepath.Dir(old), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(old, []byte("previous PDF"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := p.store.Queries.SetArchived(t.Context(), sqlc.SetArchivedParams{ID: id, CurrentPath: source, FinalPath: old, Status: StatusArchived, UpdatedAt: db.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.retryJob(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	runReprocessWork(t, p, <-p.uploadQueue)
	job, err := p.store.Queries.GetJob(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if job.FinalPath != old {
		t.Fatalf("lost previous archive: %+v", job)
	}
	return p, id, old, job.CurrentPath
}

func approveReviewAPI(t *testing.T, p *Processor, id, folder, filename, mode string) *httptest.ResponseRecorder {
	t.Helper()
	data := map[string]string{"folder": folder, "filename": filename, "document_type": "receipt"}
	if mode != "" {
		data["archive_mode"] = mode
	}
	body, _ := json.Marshal(data)
	request := httptest.NewRequest(http.MethodPost, "/api/jobs/"+id+"/approve", strings.NewReader(string(body)))
	request.SetPathValue("jobID", id)
	response := httptest.NewRecorder()
	p.handleApproveAPI(response, request)
	return response
}

func assertArchiveContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want {
		t.Fatalf("%s: got %q, %v; want %q", path, data, err, want)
	}
}

func TestApproveReprocessedArchiveChoices(t *testing.T) {
	for _, tt := range []struct {
		name, folder, filename, mode, wantName string
		keep, collision, missing               bool
	}{
		{name: "rename", folder: "Letters", filename: "renamed.pdf", mode: "replace", wantName: "renamed.pdf"},
		{name: "move", folder: "Receipts", filename: "new.pdf", mode: "replace", wantName: "new.pdf"},
		{name: "same path", folder: "Letters", filename: "old.pdf", mode: "replace", wantName: "old.pdf"},
		{name: "keep both", folder: "Letters", filename: "old.pdf", mode: "keep_both", wantName: "old-2.pdf", keep: true},
		{name: "legacy request", folder: "Letters", filename: "old.pdf", wantName: "old-2.pdf", keep: true},
		{name: "unrelated collision", folder: "Letters", filename: "new.pdf", mode: "replace", wantName: "new-2.pdf", collision: true},
		{name: "previous file already missing", folder: "Letters", filename: "new.pdf", mode: "replace", wantName: "new.pdf", missing: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p, id, old, review := archivedReviewFixture(t)
			collision := filepath.Join(p.cfg.Paths.ArchiveRoot, tt.folder, tt.filename)
			if tt.collision {
				if err := os.WriteFile(collision, []byte("unrelated PDF"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tt.missing {
				if err := os.Remove(old); err != nil {
					t.Fatal(err)
				}
			}
			response := approveReviewAPI(t, p, id, tt.folder, tt.filename, tt.mode)
			if response.Code != http.StatusOK {
				t.Fatalf("approve: %d %s", response.Code, response.Body.String())
			}
			job, err := p.store.Queries.GetJob(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(p.cfg.Paths.ArchiveRoot, tt.folder, tt.wantName)
			if job.Status != StatusArchived || job.FinalPath != want || job.CurrentPath != want {
				t.Fatalf("job: %+v", job)
			}
			assertArchiveContent(t, want, "new PDF")
			if tt.keep {
				assertArchiveContent(t, old, "previous PDF")
			} else if old != want {
				if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("old file remains: %v", err)
				}
			}
			if tt.collision {
				assertArchiveContent(t, collision, "unrelated PDF")
			}
			if _, err := os.Stat(review); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("review file remains: %v", err)
			}
			if err := filepath.WalkDir(p.cfg.Paths.ArchiveRoot, func(path string, entry os.DirEntry, err error) error {
				if err == nil && strings.HasPrefix(entry.Name(), ".paperless-") {
					t.Errorf("temporary file remains: %s", path)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestApproveReplacementFailuresPreserveFiles(t *testing.T) {
	for _, failure := range []string{"missing review", "database", "same path database", "invalid mode", "outside archive", "symlink", "shared reference", "directory"} {
		t.Run(failure, func(t *testing.T) {
			p, id, old, review := archivedReviewFixture(t)
			filename, mode := "new.pdf", "replace"
			switch failure {
			case "missing review":
				if err := os.Remove(review); err != nil {
					t.Fatal(err)
				}
			case "database", "same path database":
				if failure == "same path database" {
					filename = "old.pdf"
				}
				if _, err := p.store.Conn().ExecContext(t.Context(), `CREATE TRIGGER fail_archive BEFORE UPDATE ON jobs WHEN NEW.status='archived' BEGIN SELECT RAISE(FAIL, 'simulated database failure'); END`); err != nil {
					t.Fatal(err)
				}
			case "invalid mode":
				mode = "delete_everything"
			case "outside archive":
				old = filepath.Join(t.TempDir(), "outside.pdf")
				if err := os.WriteFile(old, []byte("previous PDF"), 0600); err != nil {
					t.Fatal(err)
				}
				if _, err := p.store.Conn().ExecContext(t.Context(), `UPDATE jobs SET final_path=? WHERE id=?`, old, id); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				outside := filepath.Join(t.TempDir(), "outside.pdf")
				if err := os.Rename(old, outside); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, old); err != nil {
					t.Fatal(err)
				}
			case "shared reference":
				if err := p.store.Queries.CreateJob(t.Context(), sqlc.CreateJobParams{ID: "another-job", CurrentPath: old, Status: StatusArchived, ScanTimestamp: db.Now(), UpdatedAt: db.Now()}); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Remove(old); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(old, 0700); err != nil {
					t.Fatal(err)
				}
			}
			response := approveReviewAPI(t, p, id, "Letters", filename, mode)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("expected failure: %d %s", response.Code, response.Body.String())
			}
			if failure != "directory" {
				assertArchiveContent(t, old, "previous PDF")
			}
			if failure != "missing review" {
				assertArchiveContent(t, review, "new PDF")
			}
			job, err := p.store.Queries.GetJob(t.Context(), id)
			if err != nil || job.Status != StatusNeedsReview || job.FinalPath != old || job.CurrentPath != review {
				t.Fatalf("lost review state: %+v %v", job, err)
			}
			if filename != "old.pdf" {
				if _, err := os.Stat(filepath.Join(p.cfg.Paths.ArchiveRoot, "Letters", filename)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("failed approval left new archive: %v", err)
				}
			}
		})
	}
}

func TestArchiveExclusiveCopyPreservesCollision(t *testing.T) {
	dir := t.TempDir()
	source, destination := filepath.Join(dir, "source.pdf"), filepath.Join(dir, "existing.pdf")
	if err := os.WriteFile(source, []byte("new PDF"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("unrelated PDF"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyArchiveExclusive(source, destination); !errors.Is(err, os.ErrExist) {
		t.Fatalf("expected collision: %v", err)
	}
	assertArchiveContent(t, destination, "unrelated PDF")
}
