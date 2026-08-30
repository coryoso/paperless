package app

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"paperless/internal/classify"
	"paperless/internal/config"
	"paperless/internal/db/sqlc"
	"paperless/internal/progress"
)

//go:embed webdist
var webAssets embed.FS

type jobView struct {
	ID                     string                  `json:"id"`
	SourceFilename         string                  `json:"source_filename"`
	ScanTimestamp          string                  `json:"scan_timestamp"`
	UpdatedAt              string                  `json:"updated_at"`
	Status                 string                  `json:"status"`
	PageCount              int64                   `json:"page_count"`
	InputKind              string                  `json:"input_kind"`
	TextSource             string                  `json:"text_source"`
	Summary                string                  `json:"summary"`
	Confidence             float64                 `json:"confidence"`
	PhysicalOriginalAction string                  `json:"physical_original_action"`
	Error                  string                  `json:"error"`
	FinalPath              string                  `json:"final_path"`
	Classification         classify.Classification `json:"classification"`
	SuggestedPath          string                  `json:"suggested_path"`
	URLs                   jobURLs                 `json:"urls"`
}

type jobURLs struct {
	Current string `json:"current"`
	Raw     string `json:"raw"`
	Text    string `json:"text"`
	Pages   string `json:"pages"`
}

type dashboardResponse struct {
	Settings   dashboardSettings `json:"settings"`
	Stats      dashboardStats    `json:"stats"`
	Folders    []string          `json:"folders"`
	ReviewJobs []jobView         `json:"review_jobs"`
	RecentJobs []jobView         `json:"recent_jobs"`
	AllJobs    []jobView         `json:"all_jobs"`
}

type dashboardSettings struct {
	Inbox               string `json:"inbox"`
	ArchiveRoot         string `json:"archive_root"`
	ArchiveExists       bool   `json:"archive_exists"`
	ScannerShareChecked bool   `json:"scanner_share_checked"`
	ScannerShareReady   bool   `json:"scanner_share_ready"`
	Model               string `json:"model"`
}

type dashboardStats struct {
	Review   int `json:"review"`
	Archived int `json:"archived"`
	Failed   int `json:"failed"`
	Total    int `json:"total"`
}

type pageView struct {
	Page     int      `json:"page"`
	Width    int      `json:"width"`
	Height   int      `json:"height"`
	ImageURL string   `json:"image_url"`
	Boxes    []ocrBox `json:"boxes"`
}

type ocrBox struct {
	Left       int     `json:"left"`
	Top        int     `json:"top"`
	Width      int     `json:"width"`
	Height     int     `json:"height"`
	Confidence float64 `json:"confidence"`
	Text       string  `json:"text"`
}

func serveDashboard(ctx context.Context, cfg config.Config) error {
	processor, cleanup, err := newProcessor(ctx, cfg)
	if err != nil {
		return err
	}
	defer cleanup()
	return processor.serve(ctx)
}

func runService(ctx context.Context, cfg config.Config) error {
	processor, cleanup, err := newProcessor(ctx, cfg)
	if err != nil {
		return err
	}
	defer cleanup()
	serverErr := make(chan error, 1)
	go func() { serverErr <- processor.serve(ctx) }()
	ticker := time.NewTicker(time.Duration(cfg.Service.PollSeconds) * time.Second)
	defer ticker.Stop()
	slog.Info("paperless service running", "inbox", cfg.Paths.Inbox)
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-serverErr:
			return err
		case <-ticker.C:
			count, err := processor.ProcessInboxOnce(ctx)
			if err != nil {
				slog.Error("inbox processing failed", "error", err)
			} else if count > 0 {
				slog.Info("processed inbox files", "count", count)
			}
		}
	}
}

func (p *Processor) serve(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/dashboard", p.handleDashboardAPI)
	mux.HandleFunc("GET /api/jobs", p.handleJobsAPI)
	mux.HandleFunc("GET /api/jobs/{jobID}", p.handleJobAPI)
	mux.HandleFunc("GET /api/jobs/{jobID}/pages", p.handleJobPagesAPI)
	mux.HandleFunc("POST /api/uploads", p.handleUploadAPI)
	mux.HandleFunc("GET /api/uploads/{runID}/events", p.handleRunEvents)
	mux.HandleFunc("POST /api/jobs/{jobID}/approve", p.handleApproveAPI)
	mux.HandleFunc("POST /api/jobs/{jobID}/reject", p.handleRejectAPI)
	mux.HandleFunc("POST /api/jobs/{jobID}/retry", p.handleRetryAPI)
	mux.HandleFunc("POST /api/folders/refresh", p.handleRefreshFoldersAPI)
	mux.HandleFunc("GET /files/{jobID}/{kind}", p.handleJobFile)
	mux.HandleFunc("GET /files/{jobID}/pages/{page}/cleaned", p.handleJobPageImage)
	mux.HandleFunc("GET /", handleWebAsset)

	addr := fmt.Sprintf("%s:%d", p.cfg.Service.Host, p.cfg.Service.Port)
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	slog.Info("dashboard listening", "url", "http://"+addr)
	err := server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (p *Processor) handleDashboardAPI(w http.ResponseWriter, r *http.Request) {
	if err := p.syncFolderInventory(r.Context()); err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	reviews, err := p.store.Queries.ListReviewJobs(r.Context(), 100)
	if err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	recent, err := p.store.Queries.ListRecentJobs(r.Context(), 8)
	if err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	all, err := p.store.Queries.ListAllJobs(r.Context())
	if err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	folders, err := p.folders(r.Context())
	if err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	share := InboxShareStatus(p.cfg.Paths.Inbox)
	response := dashboardResponse{
		Settings: dashboardSettings{
			Inbox:               p.cfg.Paths.Inbox,
			ArchiveRoot:         p.cfg.Paths.ArchiveRoot,
			ArchiveExists:       directoryExists(p.cfg.Paths.ArchiveRoot),
			ScannerShareChecked: share.Checked,
			ScannerShareReady:   share.Shared,
			Model:               p.cfg.LLM.Model,
		},
		Stats:      calculateDashboardStats(reviews, all),
		Folders:    folders,
		ReviewJobs: jobsToViews(reviews),
		RecentJobs: jobsToViews(recent),
		AllJobs:    jobsToViews(all),
	}
	writeJSON(w, http.StatusOK, response)
}

func (p *Processor) handleJobsAPI(w http.ResponseWriter, r *http.Request) {
	jobs, err := p.store.Queries.ListRecentJobs(r.Context(), 100)
	if err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, jobsToViews(jobs))
}

func (p *Processor) handleJobAPI(w http.ResponseWriter, r *http.Request) {
	job, err := p.store.Queries.GetJob(r.Context(), r.PathValue("jobID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, http.StatusOK, jobToView(job))
}

func (p *Processor) handleUploadAPI(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 100<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeAPIError(w, err, http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("document")
	if err != nil {
		writeAPIError(w, err, http.StatusBadRequest)
		return
	}
	defer file.Close()
	filename := filepath.Base(header.Filename)
	if !supportedInputs[strings.ToLower(filepath.Ext(filename))] {
		writeAPIError(w, fmt.Errorf("unsupported file type: %s", filepath.Ext(filename)), http.StatusBadRequest)
		return
	}
	runID := randomID()
	jobID := randomID()
	state := p.runs.create(runID)
	state.publish(progressEvent("upload", "received", "Upload received by server.", 4))
	uploadDir := filepath.Join(p.cfg.Paths.Processing, "uploads", runID)
	if err := os.MkdirAll(uploadDir, 0o755); err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	uploadPath := filepath.Join(uploadDir, filename)
	out, err := os.Create(uploadPath)
	if err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	_, copyErr := io.Copy(out, file)
	closeErr := out.Close()
	if copyErr != nil {
		writeAPIError(w, copyErr, http.StatusInternalServerError)
		return
	}
	if closeErr != nil {
		writeAPIError(w, closeErr, http.StatusInternalServerError)
		return
	}
	state.publish(progressEvent("upload", "saved", "Upload stored; document processing queued.", 6))
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		_, processErr := p.ProcessUploadedFile(ctx, jobID, uploadPath, state.reporter())
		state.finish(processErr)
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"run_id": runID, "job_id": jobID})
}

func (p *Processor) handleRunEvents(w http.ResponseWriter, r *http.Request) {
	runID := r.PathValue("runID")
	if !isSafeID(runID) {
		http.NotFound(w, r)
		return
	}
	state, ok := p.runs.get(runID)
	if !ok {
		http.NotFound(w, r)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, fmt.Errorf("streaming unsupported"), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	_, _ = io.WriteString(w, "retry: 1000\n\n")
	snapshot, events, done := state.subscribe()
	for _, event := range snapshot {
		if err := writeSSE(w, event); err != nil {
			if events != nil {
				state.unsubscribe(events)
			}
			return
		}
		flusher.Flush()
	}
	if done {
		return
	}
	defer state.unsubscribe(events)
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-events:
			if !open {
				return
			}
			if err := writeSSE(w, event); err != nil {
				return
			}
			flusher.Flush()
			if event.Done {
				return
			}
		}
	}
}

func (p *Processor) handleApproveAPI(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Folder                 string `json:"folder"`
		Filename               string `json:"filename"`
		DocumentType           string `json:"document_type"`
		PhysicalOriginalAction string `json:"physical_original_action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeAPIError(w, err, http.StatusBadRequest)
		return
	}
	finalPath, err := p.ApproveJob(r.Context(), r.PathValue("jobID"), input.Folder, input.Filename, input.DocumentType, input.PhysicalOriginalAction)
	if err != nil {
		writeAPIError(w, err, http.StatusBadRequest)
		return
	}
	_ = p.syncFolderInventory(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"final_path": finalPath})
}

func (p *Processor) handleRejectAPI(w http.ResponseWriter, r *http.Request) {
	if err := p.RejectJob(r.Context(), r.PathValue("jobID")); err != nil {
		writeAPIError(w, err, http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (p *Processor) handleRetryAPI(w http.ResponseWriter, r *http.Request) {
	inboxPath, err := p.retryJob(r.Context(), r.PathValue("jobID"))
	if err != nil {
		writeAPIError(w, err, http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"inbox_path": inboxPath})
}

func (p *Processor) handleRefreshFoldersAPI(w http.ResponseWriter, r *http.Request) {
	if err := p.syncFolderInventory(r.Context()); err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	folders, err := p.folders(r.Context())
	if err != nil {
		writeAPIError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string][]string{"folders": folders})
}

func (p *Processor) handleJobFile(w http.ResponseWriter, r *http.Request) {
	job, err := p.store.Queries.GetJob(r.Context(), r.PathValue("jobID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var filePath string
	switch r.PathValue("kind") {
	case "raw":
		filePath = job.RawPath
	case "current":
		filePath = job.CurrentPath
	case "text":
		filePath = job.TextPath
	default:
		http.NotFound(w, r)
		return
	}
	serveLocalFile(w, r, filePath)
}

func (p *Processor) handleJobPagesAPI(w http.ResponseWriter, r *http.Request) {
	job, err := p.store.Queries.GetJob(r.Context(), r.PathValue("jobID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	pages := make([]pageView, 0, job.PageCount)
	for pageNumber := 1; pageNumber <= int(job.PageCount); pageNumber++ {
		imagePath := filepath.Join(p.cfg.Paths.Processing, job.ID, "cleaned", fmt.Sprintf("page-%04d.png", pageNumber))
		width, height, err := imageDimensions(imagePath)
		if err != nil {
			continue
		}
		tsvPath := filepath.Join(p.cfg.Paths.Processing, job.ID, "ocr", fmt.Sprintf("page-%04d.tsv", pageNumber))
		tsv, _ := os.ReadFile(tsvPath)
		pages = append(pages, pageView{
			Page:     pageNumber,
			Width:    width,
			Height:   height,
			ImageURL: fmt.Sprintf("/files/%s/pages/%d/cleaned", job.ID, pageNumber),
			Boxes:    parseOCRBoxes(string(tsv)),
		})
	}
	writeJSON(w, http.StatusOK, map[string][]pageView{"pages": pages})
}

func (p *Processor) handleJobPageImage(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("jobID")
	if !isSafeID(jobID) {
		http.NotFound(w, r)
		return
	}
	pageNumber, err := strconv.Atoi(r.PathValue("page"))
	if err != nil || pageNumber < 1 || pageNumber > 1000 {
		http.NotFound(w, r)
		return
	}
	filePath := filepath.Join(p.cfg.Paths.Processing, jobID, "cleaned", fmt.Sprintf("page-%04d.png", pageNumber))
	serveLocalFile(w, r, filePath)
}

func jobsToViews(jobs []sqlc.Job) []jobView {
	views := make([]jobView, 0, len(jobs))
	for _, job := range jobs {
		views = append(views, jobToView(job))
	}
	return views
}

func jobToView(job sqlc.Job) jobView {
	classification := classify.Classification{}
	_ = json.Unmarshal([]byte(job.ClassificationJson), &classification)
	if classification.Reasons == nil {
		classification.Reasons = []string{}
	}
	if classification.FolderRankings == nil {
		classification.FolderRankings = []classify.FolderRanking{}
	}
	suggestedPath := classification.SuggestedFilename
	if classification.SuggestedFolder != "" {
		suggestedPath = path.Join(classification.SuggestedFolder, classification.SuggestedFilename)
	}
	base := "/files/" + job.ID
	return jobView{
		ID:                     job.ID,
		SourceFilename:         job.SourceFilename,
		ScanTimestamp:          job.ScanTimestamp,
		UpdatedAt:              job.UpdatedAt,
		Status:                 job.Status,
		PageCount:              job.PageCount,
		InputKind:              job.InputKind,
		TextSource:             job.TextSource,
		Summary:                job.Summary,
		Confidence:             job.Confidence,
		PhysicalOriginalAction: job.PhysicalOriginalAction,
		Error:                  job.Error,
		FinalPath:              job.FinalPath,
		Classification:         classification,
		SuggestedPath:          suggestedPath,
		URLs: jobURLs{
			Current: base + "/current",
			Raw:     base + "/raw",
			Text:    base + "/text",
			Pages:   "/api/jobs/" + job.ID + "/pages",
		},
	}
}

func calculateDashboardStats(reviews, all []sqlc.Job) dashboardStats {
	stats := dashboardStats{Review: len(reviews), Total: len(all)}
	for _, job := range all {
		switch job.Status {
		case StatusArchived:
			stats.Archived++
		case StatusFailed:
			stats.Failed++
		}
	}
	return stats
}

func parseOCRBoxes(tsv string) []ocrBox {
	lines := strings.Split(tsv, "\n")
	boxes := make([]ocrBox, 0)
	for index, line := range lines {
		if index == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 12 || fields[0] != "5" || strings.TrimSpace(fields[11]) == "" {
			continue
		}
		confidence, err := strconv.ParseFloat(fields[10], 64)
		if err != nil || confidence < 0 {
			continue
		}
		left, leftErr := strconv.Atoi(fields[6])
		top, topErr := strconv.Atoi(fields[7])
		width, widthErr := strconv.Atoi(fields[8])
		height, heightErr := strconv.Atoi(fields[9])
		if leftErr != nil || topErr != nil || widthErr != nil || heightErr != nil {
			continue
		}
		boxes = append(boxes, ocrBox{Left: left, Top: top, Width: width, Height: height, Confidence: confidence, Text: fields[11]})
	}
	return boxes
}

func imageDimensions(filePath string) (int, int, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	config, _, err := image.DecodeConfig(file)
	if err != nil {
		return 0, 0, err
	}
	return config.Width, config.Height, nil
}

func serveLocalFile(w http.ResponseWriter, r *http.Request, filePath string) {
	if filePath == "" {
		http.NotFound(w, r)
		return
	}
	if _, err := os.Stat(filePath); err != nil {
		http.NotFound(w, r)
		return
	}
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(filePath)))
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.Header().Set("Content-Disposition", "inline; filename=\""+strings.ReplaceAll(filepath.Base(filePath), "\"", "")+"\"")
	http.ServeFile(w, r, filePath)
}

func handleWebAsset(w http.ResponseWriter, r *http.Request) {
	assets, err := fs.Sub(webAssets, "webdist")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	requested := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if requested == "." || requested == "" {
		requested = "index.html"
	}
	content, err := fs.ReadFile(assets, requested)
	if err != nil {
		requested = "index.html"
		content, err = fs.ReadFile(assets, requested)
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if contentType := mime.TypeByExtension(path.Ext(requested)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if requested != "index.html" {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	_, _ = w.Write(content)
}

func writeSSE(w io.Writer, event progress.Event) error {
	eventName := "progress"
	if event.Done && event.Level == "error" {
		eventName = "failed"
	} else if event.Done {
		eventName = "done"
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventName, payload)
	return err
}

func progressEvent(phase, step, message string, percent int) progress.Event {
	return progress.Event{At: time.Now().UTC(), Level: "info", Phase: phase, Step: step, Message: message, Percent: percent}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeAPIError(w http.ResponseWriter, err error, status int) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func directoryExists(dir string) bool {
	info, err := os.Stat(dir)
	return err == nil && info.IsDir()
}

func isSafeID(value string) bool {
	if len(value) < 8 || len(value) > 64 {
		return false
	}
	for _, char := range value {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') && (char < '0' || char > '9') && char != '-' && char != '_' {
			return false
		}
	}
	return true
}
