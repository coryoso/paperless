package ocr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"paperless/internal/config"
	"paperless/internal/progress"
)

type Result struct {
	SearchablePDF string       `json:"searchable_pdf"`
	TextPath      string       `json:"text_path"`
	Text          string       `json:"text"`
	PageCount     int          `json:"page_count"`
	TextHash      string       `json:"text_hash"`
	InputKind     string       `json:"input_kind"`
	TextSource    string       `json:"text_source"`
	Pages         []PageResult `json:"pages"`
	Warnings      []string     `json:"warnings"`
}

type PageResult struct {
	SourceImage        string  `json:"source_image"`
	CleanedImage       string  `json:"cleaned_image"`
	SearchablePDF      string  `json:"searchable_pdf"`
	TextPath           string  `json:"text_path"`
	TSVPath            string  `json:"tsv_path"`
	Layout             string  `json:"layout"`
	ContentAreaRatio   float64 `json:"content_area_ratio"`
	CropConfidence     float64 `json:"crop_confidence"`
	Cropped            bool    `json:"cropped"`
	OrientationDegrees int     `json:"orientation_degrees"`
	DeskewAngle        float64 `json:"deskew_angle"`
	Width              int     `json:"width"`
	Height             int     `json:"height"`
}

type imageAnalysis struct {
	Bounds             image.Rectangle
	Content            image.Rectangle
	OutputBounds       image.Rectangle
	ContentAreaRatio   float64
	CropConfidence     float64
	Layout             string
	ShouldCrop         bool
	OrientationDegrees int
	DeskewAngle        float64
}

var supportedImageExt = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
}

func Process(ctx context.Context, cfg config.Config, inputPath string, workDir string) (Result, error) {
	return ProcessWithProgress(ctx, cfg, inputPath, workDir, nil)
}

func ProcessWithProgress(ctx context.Context, cfg config.Config, inputPath string, workDir string, reporter progress.Reporter) (Result, error) {
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return Result{}, err
	}
	inputKind := "scan"
	if strings.EqualFold(filepath.Ext(inputPath), ".pdf") {
		reporter.Info("ocr", "inspect", "Checking PDF for an embedded text layer.", 0, 0, 14)
		profile, err := inspectNativePDF(ctx, inputPath)
		if err == nil {
			inputKind = profile.Kind
			if profile.UseEmbeddedText {
				return preserveDigitalPDF(inputPath, workDir, profile, reporter)
			}
			reporter.Info("ocr", "inspect", profile.FallbackMessage(), 0, 0, 16)
		} else {
			reporter.Warn("ocr", "inspect", "Embedded text could not be read; falling back to OCR.", 0, 0, 16)
		}
	}
	reporter.Info("ocr", "render", "Rendering input pages.", 0, 0, 14)
	rendered, err := renderInput(ctx, cfg, inputPath, filepath.Join(workDir, "rendered"))
	if err != nil {
		return Result{}, err
	}
	if len(rendered) == 0 {
		return Result{}, errors.New("no pages rendered for OCR")
	}
	reporter.Info("ocr", "render", fmt.Sprintf("Rendered %d page(s).", len(rendered)), len(rendered), len(rendered), 22)
	reporter.Info("ocr", "languages", "Checking Tesseract language data.", 0, 0, 24)
	languages, missing, err := ResolveLanguages(ctx, cfg.OCR.Languages)
	if err != nil {
		return Result{}, err
	}
	var warnings []string
	if len(missing) > 0 {
		message := "missing Tesseract language data: " + strings.Join(missing, ", ")
		warnings = append(warnings, message)
		reporter.Warn("ocr", "languages", message, 0, 0, 26)
	}

	cleanedDir := filepath.Join(workDir, "cleaned")
	ocrDir := filepath.Join(workDir, "ocr")
	reporter.Info("ocr", "prepare", "Preparing cleaned page and OCR output folders.", 0, 0, 28)
	if err := os.MkdirAll(cleanedDir, 0o755); err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(ocrDir, 0o755); err != nil {
		return Result{}, err
	}

	var pages []PageResult
	var pagePDFs []string
	var textParts []string
	for index, imagePath := range rendered {
		page := index + 1
		totalWorkUnits := len(rendered) * 4
		pageBaseUnit := index * 4
		cleanedPath := filepath.Join(cleanedDir, fmt.Sprintf("page-%04d.png", index+1))
		reporter.Info("ocr", "clean", fmt.Sprintf("Cleaning page %d of %d.", page, len(rendered)), page, len(rendered), progress.PercentRange(30, 76, pageBaseUnit, totalWorkUnits))
		analysis, err := cleanImage(ctx, cfg, imagePath, cleanedPath)
		if err != nil {
			return Result{}, err
		}
		reporter.Info("ocr", "clean", pageCleanupMessage(page, len(rendered), analysis), page, len(rendered), progress.PercentRange(30, 76, pageBaseUnit+1, totalWorkUnits))
		base := filepath.Join(ocrDir, fmt.Sprintf("page-%04d", index+1))
		if err := runTesseract(ctx, languages, cleanedPath, base, cfg.OCR.RenderDPI, reporter, page, len(rendered), pageBaseUnit+1, totalWorkUnits); err != nil {
			return Result{}, err
		}
		textPath := base + ".txt"
		tsvPath := base + ".tsv"
		textBytes, _ := os.ReadFile(textPath)
		textParts = append(textParts, string(textBytes))
		pagePDF := base + ".pdf"
		pagePDFs = append(pagePDFs, pagePDF)
		pages = append(pages, PageResult{
			SourceImage:        imagePath,
			CleanedImage:       cleanedPath,
			SearchablePDF:      pagePDF,
			TextPath:           textPath,
			TSVPath:            tsvPath,
			Layout:             analysis.Layout,
			ContentAreaRatio:   analysis.ContentAreaRatio,
			CropConfidence:     analysis.CropConfidence,
			Cropped:            analysis.ShouldCrop,
			OrientationDegrees: analysis.OrientationDegrees,
			DeskewAngle:        analysis.DeskewAngle,
			Width:              analysis.OutputBounds.Dx(),
			Height:             analysis.OutputBounds.Dy(),
		})
	}

	reporter.Info("ocr", "merge", "Merging searchable page PDFs.", 0, 0, 80)
	outputPDF := filepath.Join(workDir, "searchable.pdf")
	if err := mergePDFs(ctx, pagePDFs, outputPDF); err != nil {
		return Result{}, err
	}
	text := strings.TrimSpace(strings.Join(textParts, "\n\f\n"))
	textPath := filepath.Join(workDir, "ocr.txt")
	reporter.Info("ocr", "text", "Writing combined OCR text.", 0, 0, 84)
	if err := os.WriteFile(textPath, []byte(text+"\n"), 0o644); err != nil {
		return Result{}, err
	}
	reporter.Info("ocr", "complete", fmt.Sprintf("OCR complete with %d page(s).", len(pagePDFs)), len(pagePDFs), len(pagePDFs), 86)
	return Result{
		SearchablePDF: outputPDF,
		TextPath:      textPath,
		Text:          text,
		PageCount:     len(pagePDFs),
		TextHash:      TextHash(text),
		InputKind:     inputKind,
		TextSource:    "ocr",
		Pages:         pages,
		Warnings:      warnings,
	}, nil
}

type nativePDFProfile struct {
	PageTexts       []string
	PageCount       int
	NativeTextPages int
	Kind            string
	UseEmbeddedText bool
}

func (p nativePDFProfile) FallbackMessage() string {
	if p.Kind == "mixed_pdf" {
		return fmt.Sprintf("Found embedded text on %d of %d pages; using OCR for consistent full-document text.", p.NativeTextPages, p.PageCount)
	}
	return "No usable embedded text found; treating PDF as a scan."
}

func inspectNativePDF(ctx context.Context, inputPath string) (nativePDFProfile, error) {
	commandPath, err := externalReadPath(inputPath)
	if err != nil {
		return nativePDFProfile{}, err
	}
	pageCount, err := pdfPageCount(ctx, commandPath)
	if err != nil {
		return nativePDFProfile{}, err
	}
	cmd := exec.CommandContext(ctx, "pdftotext", "-layout", commandPath, "-")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nativePDFProfile{}, fmt.Errorf("pdftotext failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	pageTexts := splitPDFTextPages(string(output), pageCount)
	largeImagePages, err := pdfPagesWithLargeImages(ctx, commandPath)
	if err != nil {
		return nativePDFProfile{}, err
	}
	usable := 0
	for _, pageText := range pageTexts {
		if nativeTextUsable(pageText) {
			usable++
		}
	}
	profile := nativePDFProfile{PageTexts: pageTexts, PageCount: pageCount, NativeTextPages: usable, Kind: "scan"}
	switch {
	case usable == pageCount && pageCount > 0 && len(largeImagePages) == 0:
		profile.Kind = "digital_pdf"
		profile.UseEmbeddedText = true
	case usable > 0:
		profile.Kind = "mixed_pdf"
	}
	return profile, nil
}

func pdfPagesWithLargeImages(ctx context.Context, inputPath string) (map[int]bool, error) {
	cmd := exec.CommandContext(ctx, "pdfimages", "-list", inputPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("pdfimages failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return parseLargeImagePages(string(output)), nil
}

func parseLargeImagePages(output string) map[int]bool {
	pages := map[int]bool{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || (fields[2] != "image" && fields[2] != "smask") {
			continue
		}
		page, pageErr := strconv.Atoi(fields[0])
		width, widthErr := strconv.Atoi(fields[3])
		height, heightErr := strconv.Atoi(fields[4])
		if pageErr != nil || widthErr != nil || heightErr != nil {
			continue
		}
		if width >= 700 && height >= 700 && int64(width)*int64(height) >= 1_000_000 {
			pages[page] = true
		}
	}
	return pages
}

func pdfPageCount(ctx context.Context, inputPath string) (int, error) {
	cmd := exec.CommandContext(ctx, "pdfinfo", inputPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("pdfinfo failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	for _, line := range strings.Split(string(output), "\n") {
		label, value, ok := strings.Cut(line, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(label), "Pages") {
			continue
		}
		count, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil && count > 0 {
			return count, nil
		}
	}
	return 0, errors.New("pdfinfo did not report a positive page count")
}

func splitPDFTextPages(text string, pageCount int) []string {
	parts := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\f")
	if len(parts) > pageCount && strings.TrimSpace(parts[len(parts)-1]) == "" {
		parts = parts[:len(parts)-1]
	}
	for len(parts) < pageCount {
		parts = append(parts, "")
	}
	if len(parts) > pageCount {
		parts = parts[:pageCount]
	}
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
	}
	return parts
}

func nativeTextUsable(text string) bool {
	alphanumeric := 0
	for _, char := range text {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			alphanumeric++
		}
	}
	return alphanumeric >= 24 && len(strings.Fields(text)) >= 4
}

func preserveDigitalPDF(inputPath, workDir string, profile nativePDFProfile, reporter progress.Reporter) (Result, error) {
	reporter.Info("ocr", "embedded-text", fmt.Sprintf("Using embedded text from %d page(s); OCR and image correction are not needed.", profile.PageCount), profile.PageCount, profile.PageCount, 72)
	outputPDF := filepath.Join(workDir, "searchable.pdf")
	if err := copyFile(inputPath, outputPDF); err != nil {
		return Result{}, err
	}
	text := strings.TrimSpace(strings.Join(profile.PageTexts, "\n\f\n"))
	textPath := filepath.Join(workDir, "extracted.txt")
	if err := os.WriteFile(textPath, []byte(text+"\n"), 0o644); err != nil {
		return Result{}, err
	}
	reporter.Info("ocr", "complete", fmt.Sprintf("Embedded PDF text ready with %d page(s).", profile.PageCount), profile.PageCount, profile.PageCount, 86)
	return Result{
		SearchablePDF: outputPDF,
		TextPath:      textPath,
		Text:          text,
		PageCount:     profile.PageCount,
		TextHash:      TextHash(text),
		InputKind:     "digital_pdf",
		TextSource:    "embedded",
		Pages:         []PageResult{},
		Warnings:      []string{},
	}, nil
}

func AvailableLanguages(ctx context.Context) (map[string]bool, error) {
	cmd := exec.CommandContext(ctx, "tesseract", "--list-langs")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("tesseract --list-langs failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return parseLanguages(string(output)), nil
}

func ResolveLanguages(ctx context.Context, configured []string) (string, []string, error) {
	available, err := AvailableLanguages(ctx)
	if err != nil {
		return "", nil, err
	}
	selected, missing := resolveLanguageSelection(configured, available)
	if len(selected) == 0 {
		return "", missing, fmt.Errorf("none of the configured Tesseract languages are installed: %s", strings.Join(configured, ", "))
	}
	return strings.Join(selected, "+"), missing, nil
}

func parseLanguages(output string) map[string]bool {
	available := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of available languages") {
			continue
		}
		available[line] = true
	}
	return available
}

func resolveLanguageSelection(configured []string, available map[string]bool) ([]string, []string) {
	if len(configured) == 0 {
		configured = []string{"eng"}
	}
	var selected []string
	var missing []string
	for _, language := range configured {
		language = strings.TrimSpace(language)
		if language == "" {
			continue
		}
		if available[language] {
			selected = append(selected, language)
		} else {
			missing = append(missing, language)
		}
	}
	if len(selected) == 0 && available["eng"] {
		selected = append(selected, "eng")
	}
	return selected, missing
}

func TextHash(text string) string {
	normalized := strings.Join(strings.Fields(strings.ToLower(text)), " ")
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

func renderInput(ctx context.Context, cfg config.Config, inputPath string, outDir string) ([]string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	ext := strings.ToLower(filepath.Ext(inputPath))
	if supportedImageExt[ext] {
		out := filepath.Join(outDir, "page-0001"+ext)
		if err := copyFile(inputPath, out); err != nil {
			return nil, err
		}
		if ext == ".jpg" || ext == ".jpeg" {
			pngOut := filepath.Join(outDir, "page-0001.png")
			if err := convertImageToPNG(out, pngOut); err != nil {
				return nil, err
			}
			return []string{pngOut}, nil
		}
		return []string{out}, nil
	}
	if ext != ".pdf" {
		return nil, fmt.Errorf("unsupported input type %q", ext)
	}
	pdfPath, err := externalReadPath(inputPath)
	if err != nil {
		return nil, fmt.Errorf("PDF input missing: %w", err)
	}
	prefix := externalWritePath(filepath.Join(outDir, "page"))
	cmd := exec.CommandContext(ctx, "pdftoppm", "-png", "-r", fmt.Sprint(cfg.OCR.RenderDPI), pdfPath, prefix)
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("pdftoppm failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	files, err := filepath.Glob(prefix + "-*.png")
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func cleanImage(ctx context.Context, cfg config.Config, inputPath string, outputPath string) (imageAnalysis, error) {
	input, err := os.Open(inputPath)
	if err != nil {
		return imageAnalysis{}, err
	}
	defer input.Close()
	img, _, err := image.Decode(input)
	if err != nil {
		return imageAnalysis{}, err
	}
	orientationDegrees := detectOrientation(ctx, inputPath)
	if orientationDegrees != 0 {
		img = rotateRightAngleImage(img, orientationDegrees)
	}

	// Receipts are often placed on an A4 scanner bed. Isolate the dominant narrow
	// document before deskewing so scanner-bed edges cannot drive the angle.
	initial := analyzeImage(img, cfg)
	working := img
	cropped := false
	if cfg.OCR.CropContent && initial.ShouldCrop {
		working = cropImage(working, initial.Content)
		cropped = true
	}
	deskewAngle := detectSkewAngle(working)
	if initial.Layout == "receipt-or-small-document" {
		deskewAngle = detectTextSkewAngle(working)
	}
	if math.Abs(deskewAngle) >= 0.15 {
		working = rotateImage(working, deskewAngle)
	} else {
		deskewAngle = 0
	}
	analysis := analyzeImage(working, cfg)
	if initial.Layout == "receipt-or-small-document" {
		analysis.Bounds = initial.Bounds
		analysis.Content = initial.Content
		analysis.ContentAreaRatio = initial.ContentAreaRatio
		analysis.CropConfidence = initial.CropConfidence
		analysis.Layout = initial.Layout
	}
	analysis.OrientationDegrees = orientationDegrees
	analysis.DeskewAngle = deskewAngle
	outImg := working
	if cfg.OCR.CropContent && analysis.ShouldCrop {
		outImg = cropImage(working, analysis.Content)
		cropped = true
	} else if cropped {
		// A first crop can make the second analysis look full-page. Keep the crop
		// decision visible in metadata and progress output.
		analysis.ShouldCrop = true
	}
	analysis.OutputBounds = outImg.Bounds()
	out, err := os.Create(outputPath)
	if err != nil {
		return imageAnalysis{}, err
	}
	defer out.Close()
	if err := png.Encode(out, outImg); err != nil {
		return imageAnalysis{}, err
	}
	return analysis, nil
}

func analyzeImage(img image.Image, cfg config.Config) imageAnalysis {
	bounds := img.Bounds()
	if content, ok := dominantContentBounds(img, cfg.OCR.CropPaddingPixels); ok {
		contentArea := areaRatio(content, bounds)
		widthRatio := float64(content.Dx()) / float64(bounds.Dx())
		aspect := float64(content.Dy()) / float64(max(content.Dx(), 1))
		receiptLike := widthRatio < 0.72 && aspect > 1.2 && contentArea < 0.75
		if receiptLike {
			return imageAnalysis{
				Bounds:           bounds,
				Content:          content,
				ContentAreaRatio: contentArea,
				CropConfidence:   0.95,
				Layout:           "receipt-or-small-document",
				ShouldCrop:       contentArea > 0.03,
			}
		}
	}

	minX, minY := bounds.Max.X, bounds.Max.Y
	maxX, maxY := bounds.Min.X, bounds.Min.Y
	darkCount := 0
	total := bounds.Dx() * bounds.Dy()
	step := 1
	if total > 8_000_000 {
		step = 2
	}
	for y := bounds.Min.Y; y < bounds.Max.Y; y += step {
		for x := bounds.Min.X; x < bounds.Max.X; x += step {
			if isInk(img.At(x, y)) {
				darkCount++
				if x < minX {
					minX = x
				}
				if y < minY {
					minY = y
				}
				if x > maxX {
					maxX = x
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if darkCount == 0 {
		return imageAnalysis{Bounds: bounds, Content: bounds, Layout: "blank"}
	}
	padding := cfg.OCR.CropPaddingPixels
	content := image.Rect(
		clamp(minX-padding, bounds.Min.X, bounds.Max.X),
		clamp(minY-padding, bounds.Min.Y, bounds.Max.Y),
		clamp(maxX+padding+1, bounds.Min.X, bounds.Max.X),
		clamp(maxY+padding+1, bounds.Min.Y, bounds.Max.Y),
	)
	contentArea := float64(content.Dx()*content.Dy()) / float64(bounds.Dx()*bounds.Dy())
	leftMargin := float64(content.Min.X-bounds.Min.X) / float64(bounds.Dx())
	rightMargin := float64(bounds.Max.X-content.Max.X) / float64(bounds.Dx())
	topMargin := float64(content.Min.Y-bounds.Min.Y) / float64(bounds.Dy())
	bottomMargin := float64(bounds.Max.Y-content.Max.Y) / float64(bounds.Dy())
	minMargin := math.Min(math.Min(leftMargin, rightMargin), math.Min(topMargin, bottomMargin))
	maxMargin := math.Max(math.Max(leftMargin, rightMargin), math.Max(topMargin, bottomMargin))
	receiptLike := float64(content.Dx())/float64(bounds.Dx()) < 0.7 && float64(content.Dy())/float64(content.Dx()) > 1.2
	layout := "letter"
	if receiptLike || contentArea < 0.55 && maxMargin > 0.18 {
		layout = "receipt-or-small-document"
	}
	confidence := clampFloat((maxMargin-minMargin)*1.8+0.35, 0, 1)
	shouldCrop := contentArea > 0.03 && contentArea < 0.92 && confidence >= cfg.OCR.MinCropConfidence
	return imageAnalysis{
		Bounds:           bounds,
		Content:          content,
		ContentAreaRatio: contentArea,
		CropConfidence:   confidence,
		Layout:           layout,
		ShouldCrop:       shouldCrop,
	}
}

func dominantContentBounds(img image.Image, padding int) (image.Rectangle, bool) {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w < 200 || h < 200 {
		return image.Rectangle{}, false
	}
	step := 1
	if w*h > 8_000_000 {
		step = 2
	}

	columnCounts := make([]int, w)
	topIgnore := max(h/50, 2)
	bottomIgnore := max(h/100, 2)
	padding = max(padding, min(w, h)/50)
	for y := bounds.Min.Y + topIgnore; y < bounds.Max.Y-bottomIgnore; y += step {
		for x := bounds.Min.X; x < bounds.Max.X; x += step {
			if isInk(img.At(x, y)) {
				columnCounts[x-bounds.Min.X]++
			}
		}
	}
	x0, x1, ok := dominantProjectionSpan(columnCounts, max(4, h/(step*180)), max(8, w/30))
	if !ok {
		return image.Rectangle{}, false
	}

	rowCounts := make([]int, h)
	for y := bounds.Min.Y + topIgnore; y < bounds.Max.Y-bottomIgnore; y += step {
		for x := bounds.Min.X + x0; x < bounds.Min.X+x1; x += step {
			if isInk(img.At(x, y)) {
				rowCounts[y-bounds.Min.Y]++
			}
		}
	}
	y0, y1, ok := dominantProjectionSpan(rowCounts, max(3, (x1-x0)/(step*220)), max(10, h/24))
	if !ok {
		return image.Rectangle{}, false
	}

	content := image.Rect(
		clamp(bounds.Min.X+x0-padding, bounds.Min.X, bounds.Max.X),
		clamp(bounds.Min.Y+y0-padding, bounds.Min.Y, bounds.Max.Y),
		clamp(bounds.Min.X+x1+padding, bounds.Min.X, bounds.Max.X),
		clamp(bounds.Min.Y+y1+padding, bounds.Min.Y, bounds.Max.Y),
	)
	return content, content.Dx() > 0 && content.Dy() > 0
}

func dominantProjectionSpan(counts []int, threshold, maxGap int) (int, int, bool) {
	type span struct {
		start  int
		end    int
		weight int
	}
	var spans []span
	start, last, weight := -1, -1, 0
	for index, count := range counts {
		if count < threshold {
			continue
		}
		if start < 0 || index-last > maxGap {
			if start >= 0 {
				spans = append(spans, span{start: start, end: last + 1, weight: weight})
			}
			start, weight = index, 0
		}
		last = index
		weight += count
	}
	if start >= 0 {
		spans = append(spans, span{start: start, end: last + 1, weight: weight})
	}
	if len(spans) == 0 {
		return 0, 0, false
	}
	best := spans[0]
	for _, candidate := range spans[1:] {
		if candidate.weight > best.weight {
			best = candidate
		}
	}
	return best.start, best.end, best.end > best.start
}

func areaRatio(content, bounds image.Rectangle) float64 {
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return 0
	}
	return float64(content.Dx()*content.Dy()) / float64(bounds.Dx()*bounds.Dy())
}

func isInk(c color.Color) bool {
	r, g, b, a := c.RGBA()
	if a < 0x8000 {
		return false
	}
	luma := 0.299*float64(r>>8) + 0.587*float64(g>>8) + 0.114*float64(b>>8)
	return luma < 225
}

func cropImage(img image.Image, rect image.Rectangle) image.Image {
	out := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			out.Set(x-rect.Min.X, y-rect.Min.Y, img.At(x, y))
		}
	}
	return out
}

func detectOrientation(ctx context.Context, inputPath string) int {
	commandImagePath, err := externalReadPath(inputPath)
	if err != nil {
		return 0
	}
	cmd := exec.CommandContext(ctx, "tesseract", commandImagePath, "stdout", "--psm", "0", "-l", "osd")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0
	}
	return parseOrientation(output)
}

func parseOrientation(output []byte) int {
	fallback := 0
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, "Rotate:"); ok {
			return rightAngleValue(value)
		}
		if value, ok := strings.CutPrefix(line, "Orientation in degrees:"); ok {
			fallback = rightAngleValue(value)
		}
	}
	return fallback
}

func rightAngleValue(value string) int {
	switch strings.TrimSpace(value) {
	case "90":
		return 90
	case "180":
		return 180
	case "270":
		return 270
	default:
		return 0
	}
}

type skewPoint struct {
	x float64
	y float64
}

func detectSkewAngle(img image.Image) float64 {
	edgeAngle := detectHorizontalEdgeSkewAngle(img)
	if math.Abs(edgeAngle) >= 0.15 {
		return edgeAngle
	}
	points := collectTextSkewPoints(img)
	textAngle := bestProjectionAngle(img.Bounds(), points)
	if math.Abs(textAngle) >= 0.15 {
		return textAngle
	}
	points = collectFaintSkewPoints(img)
	faintAngle := bestProjectionAngle(img.Bounds(), points)
	if math.Abs(faintAngle) >= 0.15 {
		return faintAngle
	}
	return 0
}

func detectTextSkewAngle(img image.Image) float64 {
	points := collectTextSkewPoints(img)
	if angle := bestProjectionAngle(img.Bounds(), points); math.Abs(angle) >= 0.15 {
		return angle
	}
	return bestProjectionAngle(img.Bounds(), collectFaintSkewPoints(img))
}

func detectHorizontalEdgeSkewAngle(img image.Image) float64 {
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w < 400 || h < 400 {
		return 0
	}
	step := w / 500
	if step < 3 {
		step = 3
	}
	if step > 10 {
		step = 10
	}
	startY := bounds.Min.Y + int(float64(h)*0.82)
	points := make([]skewPoint, 0, w/step)
	for x := bounds.Min.X; x < bounds.Max.X; x += step {
		for y := bounds.Max.Y - 1; y >= startY; y-- {
			if lumaValue(img.At(x, y)) < 248 {
				points = append(points, skewPoint{x: float64(x), y: float64(y)})
				break
			}
		}
	}
	minPoints := int(float64(w/step) * 0.35)
	if len(points) < minPoints {
		return 0
	}
	slope, ok := robustSlope(points)
	if !ok {
		return 0
	}
	correction := -math.Atan(slope) * 180 / math.Pi
	if math.Abs(correction) < 0.15 || math.Abs(correction) > 2.5 {
		return 0
	}
	return math.Round(correction*100) / 100
}

func robustSlope(points []skewPoint) (float64, bool) {
	if len(points) < 40 {
		return 0, false
	}
	slope, intercept, ok := lineFit(points)
	if !ok {
		return 0, false
	}
	filtered := make([]skewPoint, 0, len(points))
	for _, point := range points {
		residual := point.y - (slope*point.x + intercept)
		if math.Abs(residual) <= 24 {
			filtered = append(filtered, point)
		}
	}
	if len(filtered) < len(points)/2 || len(filtered) < 40 {
		return 0, false
	}
	slope, _, ok = lineFit(filtered)
	return slope, ok
}

func lineFit(points []skewPoint) (float64, float64, bool) {
	var sumX, sumY float64
	for _, point := range points {
		sumX += point.x
		sumY += point.y
	}
	meanX := sumX / float64(len(points))
	meanY := sumY / float64(len(points))
	var sumXX, sumXY float64
	for _, point := range points {
		dx := point.x - meanX
		sumXX += dx * dx
		sumXY += dx * (point.y - meanY)
	}
	if sumXX == 0 {
		return 0, 0, false
	}
	slope := sumXY / sumXX
	return slope, meanY - slope*meanX, true
}

func bestProjectionAngle(bounds image.Rectangle, points []skewPoint) float64 {
	if len(points) < 120 {
		return 0
	}
	cx := float64(bounds.Min.X+bounds.Max.X-1) / 2
	cy := float64(bounds.Min.Y+bounds.Max.Y-1) / 2
	bestAngle, bestScore := 0.0, math.Inf(-1)
	for angle := -3.0; angle <= 3.0001; angle += 0.25 {
		score := projectionScore(points, cx, cy, angle, bounds.Dx(), bounds.Dy())
		if score > bestScore {
			bestScore = score
			bestAngle = angle
		}
	}
	fineStart := bestAngle - 0.3
	fineEnd := bestAngle + 0.3
	for angle := fineStart; angle <= fineEnd+0.0001; angle += 0.05 {
		score := projectionScore(points, cx, cy, angle, bounds.Dx(), bounds.Dy())
		if score > bestScore {
			bestScore = score
			bestAngle = angle
		}
	}
	if math.Abs(bestAngle) < 0.15 {
		return 0
	}
	return math.Round(bestAngle*100) / 100
}

func collectTextSkewPoints(img image.Image) []skewPoint {
	return collectSkewPoints(img, func(luma float64) bool {
		return luma < 205
	})
}

func collectFaintSkewPoints(img image.Image) []skewPoint {
	return collectSkewPoints(img, func(luma float64) bool {
		return luma >= 205 && luma < 250
	})
}

func collectSkewPoints(img image.Image, keep func(luma float64) bool) []skewPoint {
	bounds := img.Bounds()
	total := bounds.Dx() * bounds.Dy()
	step := int(math.Ceil(math.Sqrt(float64(total) / 220_000)))
	if step < 1 {
		step = 1
	}
	if step > 8 {
		step = 8
	}
	points := make([]skewPoint, 0, total/(step*step*8))
	for y := bounds.Min.Y; y < bounds.Max.Y; y += step {
		for x := bounds.Min.X; x < bounds.Max.X; x += step {
			if keep(lumaValue(img.At(x, y))) {
				points = append(points, skewPoint{x: float64(x), y: float64(y)})
			}
		}
	}
	return points
}

func projectionScore(points []skewPoint, cx, cy, angle float64, imageWidth, imageHeight int) float64 {
	radians := angle * math.Pi / 180
	sin, cos := math.Sin(radians), math.Cos(radians)
	binSize := 3.0
	halfSpan := float64(imageHeight)/2*math.Abs(cos) + float64(imageWidth)/2*math.Abs(sin) + 6
	bins := make([]int, int(math.Ceil(2*halfSpan/binSize))+4)
	origin := -halfSpan
	for _, point := range points {
		y := sin*(point.x-cx) + cos*(point.y-cy)
		idx := int((y - origin) / binSize)
		if idx >= 0 && idx < len(bins) {
			bins[idx]++
		}
	}
	var score float64
	for _, count := range bins {
		score += float64(count * count)
	}
	return score
}

func lumaValue(c color.Color) float64 {
	r, g, b, a := c.RGBA()
	if a < 0x8000 {
		return 255
	}
	return 0.299*float64(r>>8) + 0.587*float64(g>>8) + 0.114*float64(b>>8)
}

func rotateRightAngleImage(img image.Image, degrees int) image.Image {
	degrees = ((degrees % 360) + 360) % 360
	if degrees == 0 {
		return img
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	var out *image.RGBA
	switch degrees {
	case 90:
		out = image.NewRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				out.Set(h-1-y, x, img.At(bounds.Min.X+x, bounds.Min.Y+y))
			}
		}
	case 180:
		out = image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				out.Set(w-1-x, h-1-y, img.At(bounds.Min.X+x, bounds.Min.Y+y))
			}
		}
	case 270:
		out = image.NewRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				out.Set(y, w-1-x, img.At(bounds.Min.X+x, bounds.Min.Y+y))
			}
		}
	default:
		return rotateImage(img, float64(degrees))
	}
	return out
}

func rotateImage(img image.Image, degrees float64) image.Image {
	if math.Abs(degrees) < 0.0001 {
		return img
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	cx := float64(w-1) / 2
	cy := float64(h-1) / 2
	radians := degrees * math.Pi / 180
	sin, cos := math.Sin(radians), math.Cos(radians)
	corners := [][2]float64{
		{-cx, -cy},
		{float64(w-1) - cx, -cy},
		{-cx, float64(h-1) - cy},
		{float64(w-1) - cx, float64(h-1) - cy},
	}
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	for _, corner := range corners {
		x := cos*corner[0] - sin*corner[1]
		y := sin*corner[0] + cos*corner[1]
		minX = math.Min(minX, x)
		minY = math.Min(minY, y)
		maxX = math.Max(maxX, x)
		maxY = math.Max(maxY, y)
	}
	outW := int(math.Ceil(maxX-minX)) + 1
	outH := int(math.Ceil(maxY-minY)) + 1
	out := image.NewRGBA(image.Rect(0, 0, outW, outH))
	fillWhite(out)
	for y := 0; y < outH; y++ {
		for x := 0; x < outW; x++ {
			rx := minX + float64(x)
			ry := minY + float64(y)
			srcX := cos*rx + sin*ry + cx
			srcY := -sin*rx + cos*ry + cy
			ix := int(math.Round(srcX))
			iy := int(math.Round(srcY))
			if ix >= 0 && ix < w && iy >= 0 && iy < h {
				out.Set(x, y, img.At(bounds.Min.X+ix, bounds.Min.Y+iy))
			}
		}
	}
	return out
}

func fillWhite(img *image.RGBA) {
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			img.Set(x, y, color.White)
		}
	}
}

func pageCleanupMessage(page, total int, analysis imageAnalysis) string {
	parts := []string{fmt.Sprintf("Page %d of %d cleaned", page, total)}
	if analysis.OrientationDegrees != 0 {
		parts = append(parts, fmt.Sprintf("reoriented %d deg", analysis.OrientationDegrees))
	}
	if analysis.DeskewAngle != 0 {
		parts = append(parts, fmt.Sprintf("deskewed %.2f deg", analysis.DeskewAngle))
	}
	if analysis.ShouldCrop {
		parts = append(parts, "cropped")
	}
	if len(parts) == 1 {
		parts = append(parts, "no rotation/crop needed")
	}
	return strings.Join(parts, ", ") + "."
}

func runTesseract(ctx context.Context, languages string, imagePath string, outputBase string, dpi int, reporter progress.Reporter, page, totalPages, completedUnits, totalUnits int) error {
	commandImagePath, err := externalReadPath(imagePath)
	if err != nil {
		return fmt.Errorf("tesseract input image missing: %w", err)
	}
	commandOutputBase := externalWritePath(outputBase)
	if dpi <= 0 {
		dpi = 300
	}
	baseArgs := []string{commandImagePath, commandOutputBase, "--dpi", fmt.Sprint(dpi), "-l", languages}
	for _, run := range []struct {
		label string
		args  []string
	}{
		{label: "pdf", args: append(append([]string(nil), baseArgs...), "pdf")},
		{label: "text", args: append([]string(nil), baseArgs...)},
		{label: "tsv", args: append(append([]string(nil), baseArgs...), "tsv")},
	} {
		reporter.Info(
			"ocr",
			"tesseract-"+run.label,
			fmt.Sprintf("Running Tesseract %s pass for page %d of %d.", run.label, page, totalPages),
			page,
			totalPages,
			progress.PercentRange(30, 76, completedUnits, totalUnits),
		)
		cmd := exec.CommandContext(ctx, "tesseract", run.args...)
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("tesseract %s output failed: %w: %s", run.label, err, strings.TrimSpace(string(output)))
		}
		completedUnits++
		reporter.Info(
			"ocr",
			"tesseract-"+run.label,
			fmt.Sprintf("Finished Tesseract %s pass for page %d of %d.", run.label, page, totalPages),
			page,
			totalPages,
			progress.PercentRange(30, 76, completedUnits, totalUnits),
		)
	}
	return nil
}

func mergePDFs(ctx context.Context, inputs []string, output string) error {
	if len(inputs) == 0 {
		return errors.New("no PDFs to merge")
	}
	if len(inputs) == 1 {
		return copyFile(inputs[0], output)
	}
	args := []string{"--empty", "--pages"}
	for _, input := range inputs {
		path, err := externalReadPath(input)
		if err != nil {
			return fmt.Errorf("qpdf input missing: %w", err)
		}
		args = append(args, path)
	}
	args = append(args, "--", externalWritePath(output))
	cmd := exec.CommandContext(ctx, "qpdf", args...)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("qpdf merge failed: %w: %s", err, strings.TrimSpace(string(combined)))
	}
	return nil
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

func externalReadPath(path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	return resolveExternalPath(path), nil
}

func externalWritePath(path string) string {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	return filepath.Join(resolveExternalPath(dir), base)
}

func resolveExternalPath(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

func convertImageToPNG(inputPath, outputPath string) error {
	input, err := os.Open(inputPath)
	if err != nil {
		return err
	}
	defer input.Close()
	img, err := jpeg.Decode(input)
	if err != nil {
		return err
	}
	output, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer output.Close()
	return png.Encode(output, img)
}

func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func clampFloat(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
