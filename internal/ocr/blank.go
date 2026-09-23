package ocr

import (
	"context"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// BlankPage combines image evidence with confident OCR. Faint reverse-side
// show-through is ignored; printed forms and images with substantial ink stay.
func BlankPage(imagePath, tsvPath string) (bool, string, error) {
	f, err := os.Open(imagePath)
	if err != nil {
		return false, "", err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return false, "", err
	}
	tsv, err := os.ReadFile(tsvPath)
	if err != nil {
		return false, "OCR evidence unavailable", nil
	}
	return blankEvidence(img, string(tsv)), "Very little dark content and no confidently recognized text", nil
}
func blankEvidence(img image.Image, tsv string) bool {
	// Ignore scanner edge marks, never infer blankness from absent OCR alone.
	b := img.Bounds()
	insetX, insetY := b.Dx()/40, b.Dy()/40
	dark, total := 0, 0
	for y := b.Min.Y + insetY; y < b.Max.Y-insetY; y += 2 {
		for x := b.Min.X + insetX; x < b.Max.X-insetX; x += 2 {
			r, g, bl, _ := img.At(x, y).RGBA()
			luminance := (299*uint64(r) + 587*uint64(g) + 114*uint64(bl)) / 1000 / 257
			total++
			if luminance < 120 {
				dark++
			}
		}
	}
	if total == 0 || float64(dark)/float64(total) > 0.0005 {
		return false
	}
	for _, line := range strings.Split(tsv, "\n") {
		columns := strings.Split(line, "\t")
		if len(columns) < 12 || columns[0] != "5" {
			continue
		}
		confidence, _ := strconv.ParseFloat(columns[10], 64)
		letters := 0
		for _, r := range columns[11] {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				letters++
			}
		}
		if confidence >= 65 && letters >= 3 {
			return false
		}
	}
	return true
}

// BlankPDFPage is used only when an embedded-text PDF has no text on this page.
// Rendering distinguishes an empty page from a photo or outlined/form content.
func BlankPDFPage(ctx context.Context, path string, page int) (bool, error) {
	dir, err := os.MkdirTemp("", "paperless-blank-")
	if err != nil {
		return false, err
	}
	defer os.RemoveAll(dir)
	base := filepath.Join(dir, "page")
	output, err := exec.CommandContext(ctx, "pdftoppm", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), "-scale-to", "1200", "-singlefile", "-png", path, base).CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("render blank-page candidate: %w: %s", err, output)
	}
	f, err := os.Open(base + ".png")
	if err != nil {
		return false, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return false, err
	}
	return blankEvidence(img, ""), nil
}
