package ocr

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tsvWord(block, par, line, x, y, width, height int, text string) string {
	return fmt.Sprintf("5\t1\t%d\t%d\t%d\t1\t%d\t%d\t%d\t%d\t35\t%s\n", block, par, line, x, y, width, height, text)
}
func TestTextLayoutSeparatesAddressFromAdjacentOfficeDetails(t *testing.T) {
	var tsv string
	left := []string{"Herrn", "Alex Example", "Musterweg 1", "12345 Berlin"}
	right := []string{"Abteilung Finanzen", "Sachbearbeiterin Muster", "Rathaus Beispiel", "Telefon 0123456789"}
	for i := range left {
		tsv += tsvWord(1, 1, i+1, 30, 100+i*25, 130, 15, left[i]) + tsvWord(1, 1, i+1, 400, 100+i*25, 200, 15, right[i])
	}
	blocks := blocksFromTSV(tsv)
	text := blocksText(blocks, false)
	if len(blocks) != 1 || blocks[0].Kind != "columns" {
		t.Fatalf("no columns: %+v", blocks)
	}
	if !strings.Contains(text, strings.Join(left, "\n")) || !strings.Contains(text, strings.Join(right, "\n")) {
		t.Fatalf("address interleaved with office: %s", text)
	}
	for _, word := range append(left, right...) {
		if strings.Count(text, word) != 1 {
			t.Fatalf("recognized text lost or duplicated: %s", word)
		}
	}
}
func TestTextLayoutKeepsAmountsWithRowsAndPreservesHeadings(t *testing.T) {
	tsv := tsvWord(1, 1, 1, 30, 20, 170, 32, "RECHNUNG")
	for i, row := range [][2]string{{"Subtotal", "100,48"}, {"Fee", "5,00"}, {"Total", "105,48"}} {
		tsv += tsvWord(2, 1, i+1, 30, 100+i*25, 100, 15, row[0]) + tsvWord(2, 1, i+1, 400, 100+i*25, 60, 15, row[1])
	}
	blocks := blocksFromTSV(tsv)
	if len(blocks) != 2 || blocks[0].Kind != "heading" || blocks[1].Kind != "table" {
		t.Fatalf("wrong structure: %+v", blocks)
	}
	if len(blocks[1].Rows) != 3 || blocks[1].Rows[2][0] != "Total" || blocks[1].Rows[2][1] != "105,48" {
		t.Fatalf("amounts detached: %+v", blocks[1])
	}
	markdown := blocksText(blocks, true)
	if !strings.Contains(markdown, "## RECHNUNG") || !strings.Contains(markdown, "| Total | 105,48 |") {
		t.Fatal(markdown)
	}
}
func TestLayoutMalformedRowsAreSkippedAndMarkupIsLiteral(t *testing.T) {
	tsv := "5\tbad\n5\t1\t1\t1\t1\t1\tbad\t10\t10\t10\t90\tinvalid\n" + tsvWord(1, 1, 1, 10, 10, 100, 15, "<script>&[text] | 25,00")
	blocks := blocksFromTSV(tsv)
	if got := blocksText(blocks, false); got != "<script>&[text] | 25,00" {
		t.Fatal(got)
	}
	markdown := blocksText(blocks, true)
	if strings.Contains(markdown, "<script>") || !strings.Contains(markdown, "&lt;script&gt;") {
		t.Fatal(markdown)
	}
}
func TestExistingTextLayoutFallsBackPerPageWithoutChangingRawText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "extracted.txt")
	raw := "First    column\f\fThird page"
	os.WriteFile(path, []byte(raw), 0600)
	doc, err := ReadTextLayout(dir, path, 3)
	if err != nil || len(doc.Pages) != 3 || doc.Pages[1].Blocks[0].Text != "" || !strings.Contains(doc.Markdown, "First    column") {
		t.Fatalf("fallback: %+v %v", doc, err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != raw {
		t.Fatal("raw text changed")
	}
}
