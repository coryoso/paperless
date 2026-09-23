package ocr

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"sort"

	xdraw "golang.org/x/image/draw"
)

// documentPageFormat returns short/long edge lengths at the common render DPI.
// Small differences in scan bounds count as the same format. Uncropped pages
// vote with their original bounds, since deskewing adds variable white borders.
// Blank pages don't vote: their scanner bed says nothing about document size.
func documentPageFormat(pages []imageAnalysis) image.Point {
	if len(pages) < 2 {
		return image.Point{}
	}
	var cleaned, sources []image.Point
	for _, page := range pages {
		sources = append(sources, pageFormat(page.SourceBounds))
		if page.Layout == "blank" {
			continue
		}
		bounds := page.SourceBounds
		if page.ShouldCrop {
			bounds = page.OutputBounds
		}
		cleaned = append(cleaned, pageFormat(bounds))
	}
	if size, ok := majorityPageFormat(cleaned); ok {
		return size
	}
	// Sparse text can produce very different crops on otherwise identical
	// sheets. Fall back to the source format when crops have no majority.
	if size, ok := majorityPageFormat(sources); ok {
		return size
	}
	// A genuinely mixed document has no dominant format. Use a canvas large
	// enough for every cleaned page, avoiding arbitrary shrinking in a tie.
	var size image.Point
	for _, page := range pages {
		bounds := pageFormat(page.OutputBounds)
		size.X = max(size.X, bounds.X)
		size.Y = max(size.Y, bounds.Y)
	}
	return size
}

func pageFormat(bounds image.Rectangle) image.Point {
	return image.Pt(min(bounds.Dx(), bounds.Dy()), max(bounds.Dx(), bounds.Dy()))
}

func majorityPageFormat(sizes []image.Point) (image.Point, bool) {
	// Sorting makes the result independent of page order, including ties.
	sizes = append([]image.Point(nil), sizes...)
	sort.Slice(sizes, func(i, j int) bool {
		if sizes[i].X == sizes[j].X {
			return sizes[i].Y < sizes[j].Y
		}
		return sizes[i].X < sizes[j].X
	})
	var widths, heights []int
	for _, candidate := range sizes {
		var groupWidths, groupHeights []int
		for _, size := range sizes {
			if similarPageEdge(candidate.X, size.X) && similarPageEdge(candidate.Y, size.Y) {
				groupWidths = append(groupWidths, size.X)
				groupHeights = append(groupHeights, size.Y)
			}
		}
		if len(groupWidths) > len(widths) {
			widths, heights = groupWidths, groupHeights
		}
	}
	if len(widths) <= len(sizes)/2 {
		return image.Point{}, false
	}
	sort.Ints(widths)
	sort.Ints(heights)
	return image.Pt(widths[len(widths)/2], heights[len(heights)/2]), true
}

func similarPageEdge(a, b int) bool {
	return a > 0 && b > 0 && float64(max(a, b))/float64(min(a, b)) <= 1.05
}

func normalizePageImage(path string, format image.Point, source image.Rectangle) (image.Rectangle, error) {
	input, err := os.Open(path)
	if err != nil {
		return image.Rectangle{}, err
	}
	img, _, err := image.Decode(input)
	input.Close()
	if err != nil {
		return image.Rectangle{}, err
	}
	// Keep the corrected page orientation, even when its text crop has a
	// different aspect ratio (for example, a short paragraph on portrait A4).
	if source.Dx() > source.Dy() {
		format.X, format.Y = format.Y, format.X
	}
	out := fitPageImage(img, format)
	if out == img {
		return out.Bounds(), nil
	}
	output, err := os.Create(path)
	if err != nil {
		return image.Rectangle{}, err
	}
	if err := png.Encode(output, out); err != nil {
		output.Close()
		return image.Rectangle{}, err
	}
	if err := output.Close(); err != nil {
		return image.Rectangle{}, err
	}
	return out.Bounds(), nil
}

// fitPageImage pads smaller crops and only scales down when required to fit.
// Uniform scaling preserves proportions and keeps all cleaned content visible.
func fitPageImage(img image.Image, size image.Point) image.Image {
	bounds := img.Bounds()
	if bounds.Size() == size {
		return img
	}
	out := image.NewRGBA(image.Rectangle{Max: size})
	draw.Draw(out, out.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	scale := math.Min(1, math.Min(float64(size.X)/float64(bounds.Dx()), float64(size.Y)/float64(bounds.Dy())))
	width := max(1, int(math.Round(float64(bounds.Dx())*scale)))
	height := max(1, int(math.Round(float64(bounds.Dy())*scale)))
	origin := image.Pt((size.X-width)/2, (size.Y-height)/2)
	target := image.Rectangle{Min: origin, Max: origin.Add(image.Pt(width, height))}
	if scale == 1 {
		draw.Draw(out, target, img, bounds.Min, draw.Over)
	} else {
		xdraw.CatmullRom.Scale(out, target, img, bounds, draw.Over, nil)
	}
	return out
}
