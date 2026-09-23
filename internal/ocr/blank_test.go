package ocr

import (
	"image"
	"image/color"
	"image/draw"
	"testing"
)

func TestBlankEvidence(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1000, 1000))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	if !blankEvidence(img, "") {
		t.Fatal("white page not detected")
	}
	// Faint show-through without legible front-side content.
	draw.Draw(img, image.Rect(100, 100, 800, 200), image.NewUniform(color.Gray{Y: 230}), image.Point{}, draw.Src)
	if !blankEvidence(img, "") {
		t.Fatal("faint show-through not detected")
	}
	if blankEvidence(img, "5\t1\t1\t1\t1\t1\t0\t0\t40\t10\t90\tHello") {
		t.Fatal("confident text discarded")
	}
	draw.Draw(img, image.Rect(100, 300, 800, 305), image.NewUniform(color.Black), image.Point{}, draw.Src)
	if blankEvidence(img, "") {
		t.Fatal("printed form treated as blank")
	}
}
