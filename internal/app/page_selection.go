package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"paperless/internal/db/sqlc"
	"paperless/internal/ocr"
)

type pageChoice struct {
	Page           int    `json:"page"`
	SuggestedBlank bool   `json:"suggested_blank"`
	Excluded       bool   `json:"excluded"`
	Reason         string `json:"reason"`
}

// Called under pagesMu. The original page numbering is stable across views.
func (p *Processor) pageChoices(ctx context.Context, job sqlc.Job) ([]pageChoice, error) {
	out := []pageChoice{}
	for page := 1; page <= int(job.PageCount); page++ {
		choice := pageChoice{Page: page}
		err := p.store.Conn().QueryRowContext(ctx, `SELECT suggested_blank,excluded,reason FROM document_pages WHERE job_id=? AND page=?`, job.ID, page).Scan(&choice.SuggestedBlank, &choice.Excluded, &choice.Reason)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			dir := filepath.Join(p.cfg.Paths.Processing, job.ID)
			blank, reason, detectErr := ocr.BlankPage(filepath.Join(dir, "cleaned", fmt.Sprintf("page-%04d.png", page)), filepath.Join(dir, "ocr", fmt.Sprintf("page-%04d.tsv", page)))
			if detectErr != nil && job.TextSource == "embedded" {
				text, readErr := os.ReadFile(job.TextPath)
				if readErr == nil {
					parts := strings.Split(string(text), "\f")
					if page <= len(parts) && strings.TrimSpace(parts[page-1]) == "" {
						blank, detectErr = ocr.BlankPDFPage(ctx, job.CurrentPath, page)
						reason = "No embedded text and very little dark content"
					}
				}
			}
			if detectErr == nil {
				choice.SuggestedBlank = blank
				choice.Excluded = blank && job.Status != StatusArchived
				if blank {
					choice.Reason = reason
				}
			} else {
				choice.Reason = "Automatic blank detection unavailable"
			}
			_, err = p.store.Conn().ExecContext(ctx, `INSERT OR IGNORE INTO document_pages(job_id,page,suggested_blank,excluded,reason) VALUES(?,?,?,?,?)`, job.ID, page, choice.SuggestedBlank, choice.Excluded, choice.Reason)
			if err != nil {
				return nil, err
			}
		}
		out = append(out, choice)
	}
	// An entirely blank input still needs review, never emit a zero-page PDF.
	all := len(out) > 0
	for _, v := range out {
		all = all && v.Excluded
	}
	if all {
		out[0].Excluded = false
		_, err := p.store.Conn().ExecContext(ctx, `UPDATE document_pages SET excluded=0 WHERE job_id=? AND page=1`, job.ID)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
func (p *Processor) handlePageSelection(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && !localRequest(r) {
		writeAPIError(w, errors.New("local requests only"), 403)
		return
	}
	p.pagesMu.Lock()
	defer p.pagesMu.Unlock()
	job, err := p.store.Queries.GetJob(r.Context(), r.PathValue("jobID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if job.Status != StatusNeedsReview && job.Status != StatusArchived {
		writeAPIError(w, errors.New("pages are not ready for review"), 409)
		return
	}
	choices, err := p.pageChoices(r.Context(), job)
	if err != nil {
		writeAPIError(w, err, 500)
		return
	}
	if r.Method == http.MethodPost {
		if job.Status != StatusNeedsReview {
			writeAPIError(w, errors.New("page selection is only editable during review"), 409)
			return
		}
		var body struct {
			Included []int `json:"included"`
		}
		if err = json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
			writeAPIError(w, err, 400)
			return
		}
		keep := map[int]bool{}
		for _, page := range body.Included {
			if page < 1 || page > len(choices) {
				writeAPIError(w, errors.New("invalid page number"), 400)
				return
			}
			keep[page] = true
		}
		if len(keep) == 0 {
			writeAPIError(w, errors.New("keep at least one page"), 400)
			return
		}
		tx, e := p.store.Conn().BeginTx(r.Context(), nil)
		if e != nil {
			writeAPIError(w, e, 500)
			return
		}
		defer tx.Rollback()
		for i := range choices {
			choices[i].Excluded = !keep[choices[i].Page]
			if _, e = tx.ExecContext(r.Context(), `UPDATE document_pages SET excluded=? WHERE job_id=? AND page=?`, choices[i].Excluded, job.ID, choices[i].Page); e != nil {
				writeAPIError(w, e, 500)
				return
			}
		}
		if e = tx.Commit(); e != nil {
			writeAPIError(w, e, 500)
			return
		}
	}
	writeJSON(w, 200, map[string]any{"pages": choices})
}
func (p *Processor) selectedPDF(ctx context.Context, job sqlc.Job) (string, int, error) {
	choices, err := p.pageChoices(ctx, job)
	if err != nil {
		return "", 0, err
	}
	keep := []string{}
	for _, v := range choices {
		if !v.Excluded {
			keep = append(keep, strconv.Itoa(v.Page))
		}
	}
	if len(keep) == len(choices) {
		return job.CurrentPath, len(keep), nil
	}
	dir := filepath.Join(p.cfg.Paths.Processing, job.ID)
	if err = os.MkdirAll(dir, 0755); err != nil {
		return "", 0, err
	}
	temp, err := os.CreateTemp(dir, "selected-*.pdf")
	if err != nil {
		return "", 0, err
	}
	path := temp.Name()
	temp.Close()
	os.Remove(path)
	// Keep the complete searchable PDF and raw scan intact until approval.
	cmd := exec.CommandContext(ctx, "qpdf", job.CurrentPath, "--pages", ".", strings.Join(keep, ","), "--", path)
	if output, e := cmd.CombinedOutput(); e != nil {
		os.Remove(path)
		return "", 0, fmt.Errorf("select PDF pages: %w: %s", e, output)
	}
	return path, len(keep), nil
}
func (p *Processor) handleSelectedPDF(w http.ResponseWriter, r *http.Request) {
	p.pagesMu.Lock()
	defer p.pagesMu.Unlock()
	job, err := p.store.Queries.GetJob(r.Context(), r.PathValue("jobID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if job.Status != StatusNeedsReview {
		serveLocalFile(w, r, job.CurrentPath)
		return
	}
	path, _, err := p.selectedPDF(r.Context(), job)
	if err != nil {
		writeAPIError(w, err, 500)
		return
	}
	if path != job.CurrentPath {
		defer os.Remove(path)
	}
	w.Header().Set("Cache-Control", "no-store")
	serveLocalFile(w, r, path)
}
