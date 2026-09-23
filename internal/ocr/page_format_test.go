package ocr

import (
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

func TestDocumentPageFormat(t *testing.T) {
	page := func(width, height, cropWidth, cropHeight int, blank bool) imageAnalysis {
		layout := "letter"
		if blank {
			layout = "blank"
		}
		return imageAnalysis{
			SourceBounds: image.Rect(0, 0, width, height),
			OutputBounds: image.Rect(0, 0, cropWidth, cropHeight),
			ShouldCrop:   cropWidth < width || cropHeight < height,
			Layout:       layout,
		}
	}
	a4 := page(2480, 3508, 2480, 3508, false)
	receipt := page(2480, 3508, 700, 2000, false)
	blank := page(2480, 3508, 2480, 3508, true)
	for _, test := range []struct {
		name  string
		pages []imageAnalysis
		want  image.Point
	}{
		{"single receipt", []imageAnalysis{receipt}, image.Point{}},
		{"single page", []imageAnalysis{a4}, image.Point{}},
		{"majority A4", []imageAnalysis{a4, receipt, a4}, image.Pt(2480, 3508)},
		{"custom majority", []imageAnalysis{receipt, a4, receipt}, image.Pt(700, 2000)},
		{"blank pages do not vote", []imageAnalysis{blank, receipt, blank}, image.Pt(700, 2000)},
		{"all blank", []imageAnalysis{blank, blank}, image.Pt(2480, 3508)},
		{"two different crops use source", []imageAnalysis{a4, receipt}, image.Pt(2480, 3508)},
		{"varying sparse crops use source", []imageAnalysis{receipt, page(2480, 3508, 1700, 1100, false), page(2480, 3508, 2200, 3000, false)}, image.Pt(2480, 3508)},
		{"deskew expansion does not change format", []imageAnalysis{page(2480, 3508, 2600, 3600, false), a4}, image.Pt(2480, 3508)},
		{"landscape uses same format", []imageAnalysis{a4, page(3508, 2480, 3508, 2480, false)}, image.Pt(2480, 3508)},
		{"nearby sizes use median", []imageAnalysis{a4, page(2470, 3500, 2470, 3500, false), page(2490, 3520, 2490, 3520, false)}, image.Pt(2480, 3508)},
		{"mixed sizes without majority", []imageAnalysis{page(1000, 1800, 1000, 1800, false), page(1200, 1500, 1200, 1500, false)}, image.Pt(1200, 1800)},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := documentPageFormat(test.pages); got != test.want {
				t.Fatalf("format = %v, want %v", got, test.want)
			}
			for i, j := 0, len(test.pages)-1; i < j; i, j = i+1, j-1 {
				test.pages[i], test.pages[j] = test.pages[j], test.pages[i]
			}
			if got := documentPageFormat(test.pages); got != test.want {
				t.Fatalf("reversing page order changed format to %v", got)
			}
		})
	}
}

func TestFitPageImagePadsWithoutResizing(t *testing.T) {
	img := image.NewRGBA(image.Rect(10, 20, 50, 80))
	fill(img, img.Bounds(), color.Black)
	out := fitPageImage(img, image.Pt(100, 120))
	if out.Bounds() != image.Rect(0, 0, 100, 120) {
		t.Fatalf("bounds = %v", out.Bounds())
	}
	// Verify every pixel, including all four margins and the complete crop.
	for y := 0; y < 120; y++ {
		for x := 0; x < 100; x++ {
			want := color.White
			if image.Pt(x, y).In(image.Rect(30, 30, 70, 90)) {
				want = color.Black
			}
			if got := color.Gray16Model.Convert(out.At(x, y)); got != want {
				t.Fatalf("pixel (%d,%d) = %v, want %v", x, y, got, want)
			}
		}
	}
}

func TestFitPageImageScalesProportionallyWithoutClipping(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	fill(img, img.Bounds(), color.Black)
	// Marks at opposing corners must both survive fitting to the canvas.
	fill(img, image.Rect(0, 0, 20, 20), color.RGBA{R: 255, A: 255})
	fill(img, image.Rect(180, 80, 200, 100), color.RGBA{B: 255, A: 255})
	out := fitPageImage(img, image.Pt(100, 100))
	for _, test := range []struct {
		x, y int
		want color.RGBA
	}{
		{2, 27, color.RGBA{R: 255, A: 255}},
		{97, 72, color.RGBA{B: 255, A: 255}},
		{50, 24, color.RGBA{R: 255, G: 255, B: 255, A: 255}},
		{50, 75, color.RGBA{R: 255, G: 255, B: 255, A: 255}},
		{50, 25, color.RGBA{A: 255}},
		{50, 74, color.RGBA{A: 255}},
	} {
		if got := color.RGBAModel.Convert(out.At(test.x, test.y)); got != test.want {
			t.Fatalf("pixel (%d,%d) = %v, want %v", test.x, test.y, got, test.want)
		}
	}
}

func TestNormalizePageImageUsesSourceOrientation(t *testing.T) {
	for _, source := range []image.Rectangle{image.Rect(0, 0, 100, 140), image.Rect(0, 0, 140, 100)} {
		path := filepath.Join(t.TempDir(), "page.png")
		// A wide text crop must not turn a portrait sheet into landscape.
		img := image.NewRGBA(image.Rect(0, 0, 80, 20))
		writePagePNG(t, path, img)
		bounds, err := normalizePageImage(path, image.Pt(100, 140), source)
		if err != nil {
			t.Fatal(err)
		}
		if bounds != source {
			t.Fatalf("bounds = %v, want %v", bounds, source)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		decoded, _, err := image.Decode(file)
		file.Close()
		if err != nil || decoded.Bounds() != bounds {
			t.Fatalf("saved image does not match metadata: %v", err)
		}
	}
}

func TestProcessNormalizesMultipageScanBeforeOCR(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping OCR integration test in short mode")
	}
	for _, tool := range []string{"tesseract", "qpdf", "pdfinfo", "pdfimages", "pdftoppm", "pdftotext"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
	languages, err := AvailableLanguages(t.Context())
	if err != nil || !languages["eng"] {
		t.Skip("Tesseract English language data is unavailable")
	}
	dir := t.TempDir()
	sparse := image.NewRGBA(image.Rect(0, 0, 900, 1100))
	fill(sparse, sparse.Bounds(), color.White)
	for y := 150; y < 900; y += 40 {
		fill(sparse, image.Rect(300, y, 500, y+6), color.Black)
	}
	var inputs []string
	for index, img := range []image.Image{syntheticTextPage(), rotateImage(syntheticTextPage(), 1.4), sparse} {
		base := filepath.Join(dir, fmt.Sprintf("input-%d", index))
		writePagePNG(t, base+".png", img)
		if err := runTesseract(t.Context(), "eng", base+".png", base, 100, nil, index+1, 3, 0, 3); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, base+".pdf")
	}
	inputPDF := filepath.Join(dir, "scan.pdf")
	if err := mergePDFs(t.Context(), inputs, inputPDF); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.OCR.RenderDPI = 100
	cfg.OCR.Languages = []string{"eng"}
	result, err := Process(t.Context(), cfg, inputPDF, filepath.Join(dir, "work"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Pages) != 3 || result.TextSource != "ocr" {
		t.Fatalf("unexpected result: pages=%d source=%q", len(result.Pages), result.TextSource)
	}
	if !result.Pages[2].Cropped || math.Abs(result.Pages[1].DeskewAngle) < 1 {
		t.Fatal("expected independent cropping and deskewing before normalization")
	}
	var pdfSize string
	for _, page := range result.Pages {
		if page.Width != result.Pages[0].Width || page.Height != result.Pages[0].Height {
			t.Fatalf("inconsistent page dimensions: %dx%d", page.Width, page.Height)
		}
		output, err := exec.CommandContext(t.Context(), "pdfinfo", page.SearchablePDF).CombinedOutput()
		if err != nil {
			t.Fatalf("pdfinfo: %v: %s", err, output)
		}
		var size string
		for _, line := range strings.Split(string(output), "\n") {
			if strings.HasPrefix(line, "Page size:") {
				size = line
			}
		}
		if size == "" || pdfSize != "" && size != pdfSize {
			t.Fatalf("inconsistent PDF sizes: %q versus %q", size, pdfSize)
		}
		pdfSize = size
	}
}

func writePagePNG(t *testing.T, path string, img image.Image) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
