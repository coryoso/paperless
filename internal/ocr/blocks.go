package ocr

import (
	"fmt"
	"os"
	"paperless/internal/document"
	"path/filepath"
	"strconv"
	"strings"
)

// ReadBlockDocument reconstructs old and new jobs from their original OCR text
// and word positions. Native text has explicit null geometry, never invented boxes.
func ReadBlockDocument(workDir, textPath string, pageCount int, multiplier float64) (document.Document, error) {
	raw, err := os.ReadFile(textPath)
	if err != nil {
		return document.Document{}, err
	}
	parts := strings.Split(string(raw), "\f")
	if pageCount < 1 {
		pageCount = len(parts)
	}
	fingerprint := append([]byte(fmt.Sprintf("blocks-v%d:%d\n", document.Version, pageCount)), raw...)
	doc := document.Document{Version: document.Version, DistanceMultiplier: multiplier, Blocks: []document.Block{}}
	for i := 0; i < pageCount; i++ {
		tsv, err := os.ReadFile(filepath.Join(workDir, "ocr", fmt.Sprintf("page-%04d.tsv", i+1)))
		if err != nil && !os.IsNotExist(err) {
			return document.Document{}, err
		}
		fingerprint = append(fingerprint, []byte(fmt.Sprintf("\npage:%d:%d\n", i, len(tsv)))...)
		fingerprint = append(fingerprint, tsv...)
		words := []document.Word{}
		for _, row := range strings.Split(string(tsv), "\n") {
			f := strings.SplitN(row, "\t", 12)
			if len(f) != 12 || f[0] != "5" || strings.TrimSpace(f[11]) == "" {
				continue
			}
			values := make([]float64, 4)
			valid := true
			for j := range values {
				v, e := strconv.ParseFloat(f[6+j], 64)
				if e != nil {
					valid = false
					break
				}
				values[j] = v
			}
			if valid {
				words = append(words, document.Word{X: values[0], Y: values[1], W: values[2], H: values[3], Text: strings.TrimSpace(f[11])})
			}
		}
		blocks := document.Cluster(words, i+1, multiplier)
		if len(blocks) == 0 && i < len(parts) {
			for _, paragraph := range strings.Split(strings.TrimSpace(parts[i]), "\n\n") {
				if text := strings.TrimSpace(paragraph); text != "" {
					blocks = append(blocks, document.Block{Page: i + 1, Representation: "paragraph", Content: text})
				}
			}
		}
		doc.Blocks = append(doc.Blocks, blocks...)
	}
	for i := range doc.Blocks {
		doc.Blocks[i].ID = i + 1
	}
	doc.SourceHash = document.Hash(fingerprint)
	return doc, nil
}
