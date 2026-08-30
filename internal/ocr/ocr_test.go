package ocr

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"paperless/internal/config"
)

func TestNativeTextUsable(t *testing.T) {
	for _, test := range []struct {
		name string
		text string
		want bool
	}{
		{name: "invoice text", text: "Example GmbH Invoice 2026-104 Total EUR 123.45", want: true},
		{name: "empty scan", text: "", want: false},
		{name: "stray PDF glyphs", text: "x 1", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := nativeTextUsable(test.text); got != test.want {
				t.Fatalf("nativeTextUsable(%q) = %v, want %v", test.text, got, test.want)
			}
		})
	}
}

func TestSplitPDFTextPagesKeepsBlankPages(t *testing.T) {
	pages := splitPDFTextPages("first page\f\fthird page\f", 3)
	if len(pages) != 3 || pages[0] != "first page" || pages[1] != "" || pages[2] != "third page" {
		t.Fatalf("pages = %#v", pages)
	}
}

func TestParseLargeImagePagesFindsSearchableScans(t *testing.T) {
	output := `page num type width height color comp bpc enc interp object ID x-ppi y-ppi size ratio
1 0 image 4912 6969 icc 3 8 jpeg yes 5 0 596 596 1931K 1.9%
1 1 image 220 80 rgb 3 8 image no 7 0 72 72 10K 5%
2 2 smask 1200 1600 gray 1 8 image no 9 0 300 300 100K 2%`
	pages := parseLargeImagePages(output)
	if !pages[1] || !pages[2] || len(pages) != 2 {
		t.Fatalf("large image pages = %#v", pages)
	}
}

func TestProcessPreservesDigitalPDFAndUsesEmbeddedText(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Poppler integration test in short mode")
	}
	for _, tool := range []string{"pdfinfo", "pdftotext"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "email-invoice.pdf")
	writeTextPDF(t, inputPath, "Example GmbH Invoice 2026-104 Total EUR 123.45 Due September")
	inputBytes, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}

	result, err := Process(t.Context(), config.Default(), inputPath, filepath.Join(dir, "work"))
	if err != nil {
		t.Fatal(err)
	}
	if result.InputKind != "digital_pdf" || result.TextSource != "embedded" {
		t.Fatalf("input metadata = %q/%q", result.InputKind, result.TextSource)
	}
	if result.PageCount != 1 || len(result.Pages) != 0 {
		t.Fatalf("page metadata = count %d, OCR pages %#v", result.PageCount, result.Pages)
	}
	if !bytes.Contains([]byte(result.Text), []byte("Invoice 2026-104")) {
		t.Fatalf("embedded text not extracted: %q", result.Text)
	}
	outputBytes, err := os.ReadFile(result.SearchablePDF)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(inputBytes, outputBytes) {
		t.Fatal("digital PDF was modified instead of being preserved")
	}
}

func writeTextPDF(t *testing.T, path, text string) {
	t.Helper()
	text = strings.NewReplacer("\\", "\\\\", "(", "\\(", ")", "\\)").Replace(text)
	stream := fmt.Sprintf("BT /F1 14 Tf 72 720 Td (%s) Tj ET", text)
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var pdf bytes.Buffer
	pdf.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = pdf.Len()
		fmt.Fprintf(&pdf, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xref := pdf.Len()
	fmt.Fprintf(&pdf, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for index := 1; index <= len(objects); index++ {
		fmt.Fprintf(&pdf, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&pdf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	if err := os.WriteFile(path, pdf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAnalyzeImageDetectsSmallReceiptContent(t *testing.T) {
	cfg := config.Default()
	img := image.NewRGBA(image.Rect(0, 0, 1000, 1400))
	fill(img, img.Bounds(), color.White)
	fill(img, image.Rect(390, 120, 610, 1280), color.Black)

	analysis := analyzeImage(img, cfg)
	if analysis.Layout != "receipt-or-small-document" {
		t.Fatalf("layout = %q", analysis.Layout)
	}
	if !analysis.ShouldCrop {
		t.Fatal("expected crop recommendation")
	}
	if analysis.ContentAreaRatio >= 0.5 {
		t.Fatalf("content ratio = %.2f", analysis.ContentAreaRatio)
	}
}

func TestAnalyzeImageIgnoresFullWidthScannerEdgeAroundReceipt(t *testing.T) {
	cfg := config.Default()
	img := image.NewRGBA(image.Rect(0, 0, 1200, 1600))
	fill(img, img.Bounds(), color.White)
	fill(img, image.Rect(0, 8, 1200, 14), color.Black)
	for y := 90; y < 1460; y += 48 {
		fill(img, image.Rect(80, y, 470, y+7), color.Black)
	}

	analysis := analyzeImage(img, cfg)
	if analysis.Layout != "receipt-or-small-document" || !analysis.ShouldCrop {
		t.Fatalf("analysis = %#v", analysis)
	}
	if analysis.Content.Max.X >= 700 {
		t.Fatalf("content bounds include scanner bed: %v", analysis.Content)
	}
}

func TestAnalyzeImageDoesNotCropFullPageContent(t *testing.T) {
	cfg := config.Default()
	img := image.NewRGBA(image.Rect(0, 0, 1000, 1400))
	fill(img, img.Bounds(), color.White)
	fill(img, image.Rect(60, 80, 940, 1320), color.Black)

	analysis := analyzeImage(img, cfg)
	if analysis.ShouldCrop {
		t.Fatal("did not expect crop recommendation")
	}
}

func TestResolveLanguageSelectionFallsBackToInstalledSubset(t *testing.T) {
	selected, missing := resolveLanguageSelection(
		[]string{"deu", "eng"},
		map[string]bool{"eng": true, "osd": true},
	)
	if len(selected) != 1 || selected[0] != "eng" {
		t.Fatalf("selected = %#v", selected)
	}
	if len(missing) != 1 || missing[0] != "deu" {
		t.Fatalf("missing = %#v", missing)
	}
}

func TestParseOrientationPrefersRotateInstruction(t *testing.T) {
	got := parseOrientation([]byte("Orientation in degrees: 270\nRotate: 90\nOrientation confidence: 7.21\n"))
	if got != 90 {
		t.Fatalf("orientation = %d", got)
	}
}

func TestDetectSkewAngleFindsSyntheticCorrection(t *testing.T) {
	img := syntheticTextPage()
	skewed := rotateImage(img, 1.6)

	angle := detectSkewAngle(skewed)
	if math.Abs(angle+1.6) > 0.35 {
		t.Fatalf("deskew angle = %.2f, want about -1.60", angle)
	}
}

func TestDetectHorizontalEdgeSkewAngleFindsBottomEdge(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1000, 1000))
	fill(img, img.Bounds(), color.White)
	drawSlopedLine(img, 70, 930, 930, math.Tan(-0.8*math.Pi/180), color.RGBA{R: 220, G: 220, B: 220, A: 255})

	angle := detectHorizontalEdgeSkewAngle(img)
	if math.Abs(angle-0.8) > 0.2 {
		t.Fatalf("edge deskew angle = %.2f, want about 0.80", angle)
	}
}

func TestCleanImageAppliesDeskewToOutput(t *testing.T) {
	cfg := config.Default()
	cfg.OCR.CropContent = false
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "input.png")
	outputPath := filepath.Join(dir, "cleaned.png")

	input, err := os.Create(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(input, rotateImage(syntheticTextPage(), 1.4)); err != nil {
		input.Close()
		t.Fatal(err)
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}

	analysis, err := cleanImage(t.Context(), cfg, inputPath, outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(analysis.DeskewAngle) < 1 {
		t.Fatalf("deskew angle = %.2f, want correction", analysis.DeskewAngle)
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("cleaned output missing: %v", err)
	}
}

func TestDetectSkewAngleLocalAcceptancePage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping local skew acceptance test in short mode")
	}
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Skip("pdftoppm is not installed")
	}
	inputPath := os.Getenv("PAPERLESS_ACCEPTANCE_PDF")
	if inputPath == "" {
		t.Skip("set PAPERLESS_ACCEPTANCE_PDF to run the local acceptance test")
	}
	if _, err := os.Stat(inputPath); err != nil {
		t.Skipf("local acceptance PDF not available: %s", inputPath)
	}
	cfg := config.Default()
	rendered, err := renderInput(t.Context(), cfg, inputPath, filepath.Join(t.TempDir(), "rendered"))
	if err != nil {
		t.Fatal(err)
	}
	deskewed := false
	for _, page := range rendered {
		file, err := os.Open(page)
		if err != nil {
			t.Fatal(err)
		}
		img, _, err := image.Decode(file)
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
		angle := detectSkewAngle(img)
		if math.Abs(angle) >= 0.15 {
			deskewed = true
		}
	}
	if !deskewed {
		t.Fatal("expected at least one rendered page to need deskew")
	}
}

func TestCleanImageLocalReceiptAcceptance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping local receipt acceptance test in short mode")
	}
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Skip("pdftoppm is not installed")
	}
	inputPath := os.Getenv("PAPERLESS_RECEIPT_ACCEPTANCE_PDF")
	if inputPath == "" {
		t.Skip("set PAPERLESS_RECEIPT_ACCEPTANCE_PDF to run the local receipt acceptance test")
	}
	if _, err := os.Stat(inputPath); err != nil {
		t.Skipf("local receipt PDF not available: %s", inputPath)
	}
	cfg := config.Default()
	dir := t.TempDir()
	rendered, err := renderInput(t.Context(), cfg, inputPath, filepath.Join(dir, "rendered"))
	if err != nil {
		t.Fatal(err)
	}
	cleanedPath := filepath.Join(dir, "cleaned.png")
	analysis, err := cleanImage(t.Context(), cfg, rendered[0], cleanedPath)
	if err != nil {
		t.Fatal(err)
	}
	if analysis.Layout != "receipt-or-small-document" || !analysis.ShouldCrop {
		t.Fatalf("receipt analysis = %#v", analysis)
	}
	if analysis.OutputBounds.Dx() >= analysis.Bounds.Dx()*3/4 {
		t.Fatalf("receipt was not narrowed: source=%v output=%v", analysis.Bounds, analysis.OutputBounds)
	}
	if float64(analysis.OutputBounds.Dy())/float64(analysis.OutputBounds.Dx()) < 1.5 {
		t.Fatalf("unexpected receipt aspect ratio: %v", analysis.OutputBounds)
	}
	t.Logf("receipt cleaned: deskew=%.2f source=%v output=%v", analysis.DeskewAngle, analysis.Bounds, analysis.OutputBounds)
}

func TestExternalPathsResolveSymlinkedParents(t *testing.T) {
	realDir := t.TempDir()
	linkBase := t.TempDir()
	linkDir := filepath.Join(linkBase, "linked")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	inputPath := filepath.Join(linkDir, "input.png")
	if err := os.WriteFile(inputPath, []byte("not an image"), 0o644); err != nil {
		t.Fatal(err)
	}

	readPath, err := externalReadPath(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	wantReadPath, err := filepath.EvalSymlinks(filepath.Join(realDir, "input.png"))
	if err != nil {
		t.Fatal(err)
	}
	if readPath != wantReadPath {
		t.Fatalf("read path = %q, want %q", readPath, wantReadPath)
	}

	writePath := externalWritePath(filepath.Join(linkDir, "output"))
	realResolved, err := filepath.EvalSymlinks(realDir)
	if err != nil {
		t.Fatal(err)
	}
	wantWritePath := filepath.Join(realResolved, "output")
	if writePath != wantWritePath {
		t.Fatalf("write path = %q, want %q", writePath, wantWritePath)
	}
}

func TestRunTesseractHandlesSymlinkedInputPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping external OCR smoke test in short mode")
	}
	if _, err := exec.LookPath("tesseract"); err != nil {
		t.Skip("tesseract is not installed")
	}
	available, err := AvailableLanguages(context.Background())
	if err != nil {
		t.Skipf("cannot list tesseract languages: %v", err)
	}
	if !available["eng"] {
		t.Skip("tesseract eng language data is not installed")
	}

	realDir := t.TempDir()
	linkBase := t.TempDir()
	linkDir := filepath.Join(linkBase, "linked")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	img := image.NewRGBA(image.Rect(0, 0, 900, 280))
	fill(img, img.Bounds(), color.White)
	fill(img, image.Rect(80, 80, 820, 115), color.Black)
	fill(img, image.Rect(80, 155, 660, 190), color.Black)
	inputPath := filepath.Join(linkDir, "page.png")
	output, err := os.Create(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(output, img); err != nil {
		output.Close()
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}

	outputBase := filepath.Join(linkDir, "page")
	if err := runTesseract(context.Background(), "eng", inputPath, outputBase, 300, nil, 1, 1, 0, 3); err != nil {
		t.Fatal(err)
	}
	for _, ext := range []string{".pdf", ".txt", ".tsv"} {
		if _, err := os.Stat(outputBase + ext); err != nil {
			t.Fatalf("expected %s output: %v", ext, err)
		}
	}
}

func fill(img *image.RGBA, rect image.Rectangle, c color.Color) {
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		for x := rect.Min.X; x < rect.Max.X; x++ {
			img.Set(x, y, c)
		}
	}
}

func syntheticTextPage() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 900, 1100))
	fill(img, img.Bounds(), color.White)
	for y := 140; y <= 820; y += 95 {
		fill(img, image.Rect(110, y, 790, y+7), color.Black)
		fill(img, image.Rect(110, y+24, 600, y+30), color.Black)
	}
	return img
}

func drawSlopedLine(img *image.RGBA, minX, baseY, maxX int, slope float64, c color.Color) {
	for x := minX; x < maxX; x++ {
		y := baseY + int(math.Round(slope*float64(x-minX)))
		fill(img, image.Rect(x, y-1, x+1, y+2), c)
	}
}
