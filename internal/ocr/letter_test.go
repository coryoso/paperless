package ocr

import (
	"image"
	"math"
	"os"
	"path/filepath"
	"testing"

	"paperless/internal/config"
)

func TestCleanImageLetterAcceptance(t *testing.T) {
	input := os.Getenv("PAPERLESS_ACCEPTANCE_LETTER_IMAGE")
	if input == "" || testing.Short() {
		t.Skip("set PAPERLESS_ACCEPTANCE_LETTER_IMAGE to check a local letter scan")
	}
	output := filepath.Join(t.TempDir(), "cleaned.png")
	analysis, err := cleanImage(t.Context(), config.Default(), input, output)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	residual := detectSkewAngle(img)
	t.Logf("correction %.2f degrees; residual %.2f degrees", analysis.DeskewAngle, residual)
	if math.Abs(residual) > .2 {
		t.Fatalf("letter still tilted by %.2f degrees", residual)
	}
}

func TestFineDeskewOnHighResolutionLetters(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2480, 3509))
	fillWhite(img)
	for y := 450; y < 2800; y += 85 {
		for x := 280; x < 2100; x += 32 {
			height := 18 + (x/32+y/85)%14
			fill(img, image.Rect(x, y-height, x+12, y), image.Black)
		}
	}
	for _, angle := range []float64{-.55, 0, .35, .6} {
		got := detectSkewAngle(rotateImage(img, angle))
		if math.Abs(got+angle) > .2 {
			t.Fatalf("correction %.2f for %.2f-degree high-resolution text", got, angle)
		}
	}
}
