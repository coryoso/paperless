package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"paperless/internal/classify"
	"paperless/internal/config"
	"paperless/internal/db"
	"paperless/internal/db/sqlc"
	"paperless/internal/ocr"
	"paperless/internal/policy"
	"paperless/internal/progress"
)

const (
	StatusReceived    = "received"
	StatusCopyingRaw  = "copying_raw"
	StatusProcessing  = "processing"
	StatusOCRComplete = "ocr_complete"
	StatusClassified  = "classified"
	StatusArchived    = "archived"
	StatusNeedsReview = "needs_review"
	StatusFailed      = "failed"
	StatusDuplicate   = "duplicate"
	StatusRejected    = "rejected"
)

var supportedInputs = map[string]bool{
	".pdf":  true,
	".png":  true,
	".jpg":  true,
	".jpeg": true,
}

type Processor struct {
	cfg   config.Config
	store *db.Store
	runs  *runRegistry
}

type DryRunResult struct {
	RunID          string                  `json:"run_id"`
	OriginalPath   string                  `json:"original_path"`
	SuggestedPath  string                  `json:"suggested_path"`
	WouldAutoFile  bool                    `json:"would_auto_file"`
	PolicyReasons  []string                `json:"policy_reasons"`
	TextPath       string                  `json:"text_path"`
	OCRPDFPath     string                  `json:"ocr_pdf_path"`
	OCRText        string                  `json:"ocr_text"`
	OCR            ocr.Result              `json:"ocr"`
	Classification classify.Classification `json:"classification"`
}

func ProcessOnce(ctx context.Context, cfg config.Config) (int, error) {
	processor, cleanup, err := newProcessor(ctx, cfg)
	if err != nil {
		return 0, err
	}
	defer cleanup()
	return processor.ProcessInboxOnce(ctx)
}

func DryRunFile(ctx context.Context, cfg config.Config, path string) (DryRunResult, error) {
	processor, cleanup, err := newProcessor(ctx, cfg)
	if err != nil {
		return DryRunResult{}, err
	}
	defer cleanup()
	return processor.DryRunFile(ctx, path)
}

func newProcessor(ctx context.Context, cfg config.Config) (*Processor, func(), error) {
	if err := cfg.EnsureDirs(); err != nil {
		return nil, nil, err
	}
	store, err := db.Open(ctx, cfg.DBPath())
	if err != nil {
		return nil, nil, err
	}
	processor := &Processor{cfg: cfg, store: store, runs: newRunRegistry()}
	if err := processor.syncFolderInventory(ctx); err != nil {
		store.Close()
		return nil, nil, err
	}
	return processor, func() { store.Close() }, nil
}

func (p *Processor) ProcessInboxOnce(ctx context.Context) (int, error) {
	paths, err := p.candidateFiles()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, path := range paths {
		stable, err := stableFile(ctx, path, time.Duration(p.cfg.Service.FileStabilitySeconds)*time.Second)
		if err != nil {
			return count, err
		}
		if !stable {
			continue
		}
		if _, err := p.ProcessFile(ctx, path); err != nil {
			slog.Error("processing failed", "path", path, "error", err)
		}
		count++
	}
	return count, nil
}

func (p *Processor) ProcessFile(ctx context.Context, inboxPath string) (string, error) {
	return p.processFile(ctx, randomID(), inboxPath, false, nil)
}

func (p *Processor) ProcessUploadedFile(ctx context.Context, jobID, uploadPath string, reporter progress.Reporter) (string, error) {
	if jobID == "" {
		jobID = randomID()
	}
	return p.processFile(ctx, jobID, uploadPath, true, reporter)
}

func (p *Processor) processFile(ctx context.Context, jobID, inboxPath string, forceReview bool, reporter progress.Reporter) (string, error) {
	info, err := os.Stat(inboxPath)
	if err != nil {
		return "", err
	}
	reporter.Info("prepare", "database", "Creating document record.", 0, 0, 8)
	now := db.Now()
	if err := p.store.Queries.CreateJob(ctx, sqlc.CreateJobParams{
		ID:             jobID,
		SourceFilename: filepath.Base(inboxPath),
		CurrentPath:    inboxPath,
		ScanTimestamp:  info.ModTime().UTC().Format(time.RFC3339),
		UpdatedAt:      now,
		Status:         StatusReceived,
	}); err != nil {
		return "", err
	}
	_ = p.store.Queries.AddEvent(ctx, sqlc.AddEventParams{
		JobID: jobID, CreatedAt: now, Level: "info", Message: "received " + filepath.Base(inboxPath),
	})
	if err := p.processJob(ctx, jobID, inboxPath, info.ModTime(), forceReview, reporter); err != nil {
		_ = p.failJob(ctx, jobID, err)
		return jobID, err
	}
	return jobID, nil
}

func (p *Processor) processJob(ctx context.Context, jobID, inboxPath string, scanTime time.Time, forceReview bool, reporter progress.Reporter) error {
	timestamp := time.Now().Format("20060102-150405")
	sourceName := safePDFName(filepath.Base(inboxPath))
	reporter.Info("prepare", "raw", "Keeping an untouched raw copy.", 0, 0, 10)
	rawPath := uniquePath(filepath.Join(p.cfg.Paths.Raw, timestamp+"__"+jobID[:8]+"__"+sourceName))
	if err := copyFile(inboxPath, rawPath); err != nil {
		return err
	}
	hash, err := sha256File(rawPath)
	if err != nil {
		return err
	}
	if err := p.store.Queries.SetRawCopy(ctx, sqlc.SetRawCopyParams{
		RawPath: rawPath, FileHash: hash, Status: StatusCopyingRaw, UpdatedAt: db.Now(), ID: jobID,
	}); err != nil {
		return err
	}

	duplicate, err := p.store.Queries.FindDuplicateByHash(ctx, sqlc.FindDuplicateByHashParams{FileHash: hash, ID: jobID})
	if err == nil && duplicate.ID != "" {
		duplicatePath := uniquePath(filepath.Join(p.cfg.Paths.Duplicates, timestamp+"__"+jobID[:8]+"__"+sourceName))
		if err := moveFile(inboxPath, duplicatePath); err != nil {
			return err
		}
		return p.store.Queries.SetDuplicate(ctx, sqlc.SetDuplicateParams{
			CurrentPath:            duplicatePath,
			Status:                 StatusDuplicate,
			DuplicateOf:            duplicate.ID,
			PhysicalOriginalAction: "discard_candidate",
			UpdatedAt:              db.Now(),
			ID:                     jobID,
		})
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	processingInput := uniquePath(filepath.Join(p.cfg.Paths.Processing, timestamp+"__"+jobID[:8]+"__"+sourceName))
	reporter.Info("prepare", "processing", "Moving document into the processing workspace.", 0, 0, 12)
	if err := moveFile(inboxPath, processingInput); err != nil {
		return err
	}
	if err := p.store.Queries.SetCurrentStatus(ctx, sqlc.SetCurrentStatusParams{
		CurrentPath: processingInput, Status: StatusProcessing, UpdatedAt: db.Now(), ID: jobID,
	}); err != nil {
		return err
	}

	workDir := filepath.Join(p.cfg.Paths.Processing, jobID)
	ocrResult, err := ocr.ProcessWithProgress(ctx, p.cfg, processingInput, workDir, reporter)
	if err != nil {
		return err
	}
	if err := p.store.Queries.SetOCRComplete(ctx, sqlc.SetOCRCompleteParams{
		CurrentPath: ocrResult.SearchablePDF,
		TextPath:    ocrResult.TextPath,
		TextHash:    ocrResult.TextHash,
		PageCount:   int64(ocrResult.PageCount),
		InputKind:   ocrResult.InputKind,
		TextSource:  ocrResult.TextSource,
		Status:      StatusOCRComplete,
		UpdatedAt:   db.Now(),
		ID:          jobID,
	}); err != nil {
		return err
	}

	folders, err := p.folders(ctx)
	if err != nil {
		return err
	}
	routingFolders := candidateFolders(ocrResult.Text, folders, 24)
	reporter.Info("classify", "folders", fmt.Sprintf("Selected %d of %d existing archive folders for routing.", len(routingFolders), len(folders)), len(routingFolders), len(folders), 87)
	classification := classify.ClassifyWithProgress(ctx, p.cfg, ocrResult.Text, filepath.Base(inboxPath), scanTime, routingFolders, reporter)
	classificationJSON, _ := json.Marshal(classification)
	if err := p.store.Queries.SetClassified(ctx, sqlc.SetClassifiedParams{
		ClassificationJson:     string(classificationJSON),
		Confidence:             classification.Confidence,
		Summary:                classification.Summary,
		PhysicalOriginalAction: classification.PhysicalOriginalAction,
		Status:                 StatusClassified,
		UpdatedAt:              db.Now(),
		ID:                     jobID,
	}); err != nil {
		return err
	}

	decision := policy.Evaluate(ctx, p.cfg, p.store.Queries, classification)
	if decision.AutoFile && !forceReview {
		finalPath, err := p.finalPath(classification.SuggestedFolder, classification.SuggestedFilename)
		if err != nil {
			return err
		}
		reporter.Info("archive", "move", "Moving document into the archive.", 0, 0, 98)
		if err := moveFile(ocrResult.SearchablePDF, finalPath); err != nil {
			return err
		}
		if err := p.store.Queries.SetArchived(ctx, sqlc.SetArchivedParams{
			CurrentPath: finalPath, FinalPath: finalPath, Status: StatusArchived, UpdatedAt: db.Now(), ID: jobID,
		}); err != nil {
			return err
		}
		_ = p.store.Queries.AddEvent(ctx, sqlc.AddEventParams{
			JobID: jobID, CreatedAt: db.Now(), Level: "info", Message: "archived to " + finalPath,
		})
		return nil
	}

	reviewName := jobID[:8] + "__" + safePDFName(classification.SuggestedFilename)
	reporter.Info("review", "queue", "Adding document to the review queue.", 0, 0, 98)
	reviewPath := uniquePath(filepath.Join(p.cfg.Paths.Review, reviewName))
	if err := moveFile(ocrResult.SearchablePDF, reviewPath); err != nil {
		return err
	}
	if err := p.store.Queries.SetNeedsReview(ctx, sqlc.SetNeedsReviewParams{
		CurrentPath: reviewPath, Status: StatusNeedsReview, UpdatedAt: db.Now(), ID: jobID,
	}); err != nil {
		return err
	}
	_ = p.store.Queries.AddEvent(ctx, sqlc.AddEventParams{
		JobID: jobID, CreatedAt: db.Now(), Level: "info", Message: "needs review: " + strings.Join(decision.Reasons, "; "),
	})
	return nil
}

func (p *Processor) DryRunFile(ctx context.Context, path string) (DryRunResult, error) {
	return p.dryRunFile(ctx, path, randomID(), nil)
}

func (p *Processor) dryRunFile(ctx context.Context, path string, runID string, reporter progress.Reporter) (DryRunResult, error) {
	if runID == "" {
		runID = randomID()
	}
	workDir := filepath.Join(p.cfg.Paths.Processing, "dry-run", runID)
	reporter.Info("prepare", "workspace", "Preparing dry-run workspace.", 0, 0, 8)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return DryRunResult{}, err
	}
	inputPath := filepath.Join(workDir, filepath.Base(path))
	reporter.Info("prepare", "copy", "Copying uploaded document into workspace.", 0, 0, 10)
	if err := copyFile(path, inputPath); err != nil {
		return DryRunResult{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return DryRunResult{}, err
	}
	reporter.Info("ocr", "start", "Starting OCR pipeline.", 0, 0, 12)
	ocrResult, err := ocr.ProcessWithProgress(ctx, p.cfg, inputPath, workDir, reporter)
	if err != nil {
		return DryRunResult{}, err
	}
	reporter.Info("classify", "folders", "Loading archive folders for routing.", 0, 0, 87)
	folders, err := p.folders(ctx)
	if err != nil {
		return DryRunResult{}, err
	}
	routingFolders := candidateFolders(ocrResult.Text, folders, 24)
	classification := classify.ClassifyWithProgress(ctx, p.cfg, ocrResult.Text, filepath.Base(path), info.ModTime(), routingFolders, reporter)
	reporter.Info("policy", "evaluate", "Checking auto-file policy.", 0, 0, 97)
	decision := policy.Evaluate(ctx, p.cfg, p.store.Queries, classification)
	suggestedPath := classification.SuggestedFilename
	if classification.SuggestedFolder != "" {
		suggestedPath = filepath.Join(classification.SuggestedFolder, classification.SuggestedFilename)
	}
	return DryRunResult{
		RunID:          runID,
		OriginalPath:   path,
		SuggestedPath:  suggestedPath,
		WouldAutoFile:  decision.AutoFile,
		PolicyReasons:  decision.Reasons,
		TextPath:       ocrResult.TextPath,
		OCRPDFPath:     ocrResult.SearchablePDF,
		OCRText:        ocrResult.Text,
		OCR:            ocrResult,
		Classification: classification,
	}, nil
}

func (p *Processor) ApproveJob(ctx context.Context, jobID, folder, filename, documentType, physicalAction string) (string, error) {
	job, err := p.store.Queries.GetJob(ctx, jobID)
	if err != nil {
		return "", err
	}
	if folder == "" {
		return "", errors.New("folder is required")
	}
	documentType = classify.NormalizeDocumentType(documentType)
	if documentType == "" {
		return "", errors.New("document type is required")
	}
	finalPath, err := p.finalPath(folder, filename)
	if err != nil {
		return "", err
	}
	var c classify.Classification
	_ = json.Unmarshal([]byte(job.ClassificationJson), &c)
	c.DocumentType = documentType
	c.SuggestedFolder = strings.Trim(folder, "/")
	c.SuggestedFilename = filepath.Base(finalPath)
	c.PhysicalOriginalAction = physicalAction
	c.Sensitive = classify.DocumentTypeSensitive(documentType)
	classificationJSON, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	if err := p.store.Queries.SetClassified(ctx, sqlc.SetClassifiedParams{
		ClassificationJson:     string(classificationJSON),
		Confidence:             job.Confidence,
		Summary:                job.Summary,
		PhysicalOriginalAction: physicalAction,
		Status:                 job.Status,
		UpdatedAt:              db.Now(),
		ID:                     jobID,
	}); err != nil {
		return "", err
	}
	if err := moveFile(job.CurrentPath, finalPath); err != nil {
		return "", err
	}
	if err := p.store.Queries.SetManualArchived(ctx, sqlc.SetManualArchivedParams{
		CurrentPath:            finalPath,
		FinalPath:              finalPath,
		Status:                 StatusArchived,
		PhysicalOriginalAction: physicalAction,
		ManualOverride:         db.BoolInt(true),
		UpdatedAt:              db.Now(),
		ID:                     jobID,
	}); err != nil {
		return "", err
	}
	if err := p.store.LearnApproval(ctx, db.Approval{
		JobID:        jobID,
		Sender:       c.Sender,
		Recipient:    c.Recipient,
		DocumentType: c.DocumentType,
		Folder:       folder,
		Filename:     filename,
		Weight:       1,
	}); err != nil {
		return "", err
	}
	return finalPath, nil
}

func (p *Processor) RejectJob(ctx context.Context, jobID string) error {
	job, err := p.store.Queries.GetJob(ctx, jobID)
	if err != nil {
		return err
	}
	rejectedPath := uniquePath(filepath.Join(p.cfg.Paths.Rejected, jobID[:8]+"__"+filepath.Base(job.CurrentPath)))
	if job.CurrentPath != "" {
		if err := moveFile(job.CurrentPath, rejectedPath); err != nil {
			return err
		}
	}
	return p.store.Queries.SetRejected(ctx, sqlc.SetRejectedParams{
		CurrentPath:            rejectedPath,
		Status:                 StatusRejected,
		PhysicalOriginalAction: "review",
		ManualOverride:         db.BoolInt(true),
		UpdatedAt:              db.Now(),
		ID:                     jobID,
	})
}

func (p *Processor) retryJob(ctx context.Context, jobID string) (string, error) {
	job, err := p.store.Queries.GetJob(ctx, jobID)
	if err != nil {
		return "", err
	}
	if job.RawPath == "" {
		return "", errors.New("job has no raw file")
	}
	retryPath := uniquePath(filepath.Join(p.cfg.Paths.Inbox, "retry-"+jobID[:8]+"-"+filepath.Base(job.RawPath)))
	if err := copyFile(job.RawPath, retryPath); err != nil {
		return "", err
	}
	return retryPath, nil
}

func (p *Processor) failJob(ctx context.Context, jobID string, cause error) error {
	_ = p.store.Queries.AddEvent(ctx, sqlc.AddEventParams{
		JobID: jobID, CreatedAt: db.Now(), Level: "error", Message: cause.Error(),
	})
	return p.store.Queries.SetFailed(ctx, sqlc.SetFailedParams{
		Status:                 StatusFailed,
		Error:                  cause.Error(),
		PhysicalOriginalAction: "review",
		UpdatedAt:              db.Now(),
		ID:                     jobID,
	})
}

func (p *Processor) candidateFiles() ([]string, error) {
	entries, err := os.ReadDir(p.cfg.Paths.Inbox)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(p.cfg.Paths.Inbox, entry.Name())
		if supportedInputs[strings.ToLower(filepath.Ext(path))] {
			files = append(files, path)
		}
	}
	sort.Strings(files)
	return files, nil
}

func (p *Processor) folders(ctx context.Context) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, folder := range p.cfg.Policy.KnownFolders {
		folder = strings.Trim(folder, "/")
		if folder != "" && !seen[folder] {
			seen[folder] = true
			out = append(out, folder)
		}
	}
	root := p.cfg.Paths.ArchiveRoot
	if info, statErr := os.Stat(root); statErr == nil && info.IsDir() {
		walkErr := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.IsDir() || path == root {
				return nil
			}
			if strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			if depth(rel) > 5 {
				return filepath.SkipDir
			}
			folder := filepath.ToSlash(rel)
			if !seen[folder] {
				seen[folder] = true
				out = append(out, folder)
			}
			return nil
		})
		if walkErr != nil {
			return nil, walkErr
		}
	}
	sort.Strings(out)
	return out, nil
}

func candidateFolders(text string, folders []string, limit int) []string {
	if limit <= 0 || len(folders) <= limit {
		return append([]string(nil), folders...)
	}
	haystack := classify.Slug(text)
	receiptLike := classify.ReceiptLikely(text)
	type scoredFolder struct {
		path  string
		score int
		depth int
	}
	scored := make([]scoredFolder, 0, len(folders))
	for _, folder := range folders {
		score := 0
		folderSlug := classify.Slug(folder)
		for _, token := range strings.Split(folderSlug, "-") {
			if len(token) >= 4 && strings.Contains(haystack, token) {
				score += len(token)
			}
		}
		if folderSlug != "" && strings.Contains(haystack, folderSlug) {
			score += 30
		}
		if receiptLike && containsAnyString(folderSlug, "belege", "receipt", "quittung") {
			score += 100
		}
		scored = append(scored, scoredFolder{path: folder, score: score, depth: depth(folder)})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		if scored[i].depth != scored[j].depth {
			return scored[i].depth < scored[j].depth
		}
		return scored[i].path < scored[j].path
	})
	selected := make([]string, 0, limit)
	for _, candidate := range scored[:limit] {
		selected = append(selected, candidate.path)
	}
	sort.Strings(selected)
	return selected
}

func containsAnyString(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func (p *Processor) syncFolderInventory(ctx context.Context) error {
	now := db.Now()
	if err := p.store.Queries.DeleteUnapprovedConfigFolders(ctx); err != nil {
		return err
	}
	for _, folder := range p.cfg.Policy.KnownFolders {
		if err := p.store.Queries.UpsertFolder(ctx, sqlc.UpsertFolderParams{
			Path: strings.Trim(folder, "/"), Source: "config", FirstSeen: now, LastSeen: now,
		}); err != nil {
			return err
		}
	}
	root := p.cfg.Paths.ArchiveRoot
	if _, err := os.Stat(root); err != nil {
		return nil
	}
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() || path == root {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if strings.HasPrefix(filepath.Base(path), ".") {
			return filepath.SkipDir
		}
		if depth(rel) > 5 {
			return filepath.SkipDir
		}
		return p.store.Queries.UpsertFolder(ctx, sqlc.UpsertFolderParams{
			Path: filepath.ToSlash(rel), Source: "archive", FirstSeen: now, LastSeen: now,
		})
	}); err != nil {
		return err
	}
	return p.store.Queries.DeleteStaleArchiveFolders(ctx, now)
}

func (p *Processor) finalPath(folder, filename string) (string, error) {
	root := p.cfg.Paths.ArchiveRoot
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("archive root is unavailable: %s", root)
	}
	folder = strings.TrimSpace(strings.Trim(folder, "/"))
	if folder == "" {
		return "", errors.New("folder is required")
	}
	destination := filepath.Clean(filepath.Join(root, filepath.FromSlash(folder)))
	rel, err := filepath.Rel(root, destination)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("folder must be a relative path inside the archive root")
	}
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return "", err
	}
	if filename == "" {
		filename = "scan.pdf"
	}
	return uniquePath(filepath.Join(destination, safePDFName(filename))), nil
}

func stableFile(ctx context.Context, path string, wait time.Duration) (bool, error) {
	first, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-timer.C:
	}
	second, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return first.Size() == second.Size() && first.ModTime().Equal(second.ModTime()), nil
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func moveFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyFile(src, dst); err != nil {
		return err
	}
	return os.Remove(src)
}

func uniquePath(path string) string {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return path
	}
	ext := filepath.Ext(path)
	stem := strings.TrimSuffix(path, ext)
	for i := 2; i < 10000; i++ {
		candidate := fmt.Sprintf("%s-%d%s", stem, i, ext)
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
	return path
}

func safePDFName(name string) string {
	stem := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	stem = safeFilenameStem(stem)
	if stem == "" {
		stem = "scan"
	}
	return stem + ".pdf"
}

func safeFilenameStem(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out strings.Builder
	lastDash := false
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			out.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			out.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(out.String(), "-")
}

func randomID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes[:])
}

func depth(path string) int {
	if path == "." || path == "" {
		return 0
	}
	return len(strings.Split(filepath.ToSlash(path), "/"))
}
