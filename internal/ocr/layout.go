package ocr

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// TextLayout retains recognized words and estimated structure. Raw OCR is kept
// separately; layout reconstruction never corrects or invents document wording.
type TextLayout struct {
	Pages    []TextPage `json:"pages"`
	Markdown string     `json:"markdown"`
	Text     string     `json:"text"`
}
type TextPage struct {
	Page   int         `json:"page"`
	Blocks []TextBlock `json:"blocks"`
}
type TextBlock struct {
	Kind    string        `json:"kind"`
	Text    string        `json:"text,omitempty"`
	Rows    [][]string    `json:"rows,omitempty"`
	Columns [][]TextBlock `json:"columns,omitempty"`
}
type layoutWord struct {
	x, y, w, h, block, par, line int
	text                         string
}
type layoutLine struct {
	words []layoutWord
	par   int
}

// ReadTextLayout also works for existing jobs, using the TSV already stored
// beside their OCR. Embedded PDF text and missing geometry retain fixed spacing.
func ReadTextLayout(workDir, textPath string, pageCount int) (TextLayout, error) {
	raw, err := os.ReadFile(textPath)
	if err != nil {
		return TextLayout{}, err
	}
	parts := strings.Split(string(raw), "\f")
	doc := TextLayout{Pages: []TextPage{}}
	for i := 0; i < pageCount; i++ {
		text := ""
		if i < len(parts) {
			text = strings.TrimSpace(parts[i])
		}
		tsv, _ := os.ReadFile(filepath.Join(workDir, "ocr", fmt.Sprintf("page-%04d.tsv", i+1)))
		blocks := blocksFromTSV(string(tsv))
		if len(blocks) == 0 {
			blocks = []TextBlock{{Kind: "pre", Text: text}}
		}
		doc.Pages = append(doc.Pages, TextPage{Page: i + 1, Blocks: blocks})
	}
	doc.finish()
	return doc, nil
}

func (doc *TextLayout) finish() {
	var plain, markdown []string
	for _, page := range doc.Pages {
		plain = append(plain, blocksText(page.Blocks, false))
		markdown = append(markdown, blocksText(page.Blocks, true))
	}
	doc.Text = strings.Join(plain, "\n\f\n")
	doc.Markdown = strings.Join(markdown, "\n\n---\n\n")
}

func blocksFromTSV(tsv string) []TextBlock {
	groups := map[int][]layoutWord{}
	heights := []int{}
	for _, row := range strings.Split(tsv, "\n") {
		f := strings.SplitN(row, "\t", 12)
		if len(f) != 12 || f[0] != "5" || strings.TrimSpace(f[11]) == "" {
			continue
		}
		v := make([]int, 10)
		valid := true
		for _, i := range []int{2, 3, 4, 6, 7, 8, 9} {
			n, err := strconv.Atoi(f[i])
			if err != nil {
				valid = false
				break
			}
			v[i] = n
		}
		if !valid || v[6] < 0 || v[7] < 0 || v[8] <= 0 || v[9] <= 0 {
			continue
		}
		w := layoutWord{x: v[6], y: v[7], w: v[8], h: v[9], block: v[2], par: v[3], line: v[4], text: strings.TrimSpace(f[11])}
		groups[w.block] = append(groups[w.block], w)
		heights = append(heights, w.h)
	}
	if len(heights) == 0 {
		return nil
	}
	sort.Ints(heights)
	height := heights[len(heights)/2]
	blocks := make([][]layoutWord, 0, len(groups))
	for _, words := range groups {
		blocks = append(blocks, words)
	}
	sort.SliceStable(blocks, func(i, j int) bool {
		xi, yi, _, _ := wordBounds(blocks[i])
		xj, yj, _, _ := wordBounds(blocks[j])
		if yi == yj {
			return xi < xj
		}
		return yi < yj
	})
	var out []TextBlock
	previousBottom := 0
	for _, words := range blocks {
		_, top, _, bottom := wordBounds(words)
		next := spatialBlocks(words, height, 0)
		if len(out) > 0 && len(next) > 0 && top-previousBottom < height*2 && out[len(out)-1].Kind == "table" && next[0].Kind == "table" && len(out[len(out)-1].Rows[0]) == len(next[0].Rows[0]) {
			out[len(out)-1].Rows = append(out[len(out)-1].Rows, next[0].Rows...)
			next = next[1:]
		}
		out = append(out, next...)
		previousBottom = bottom
	}
	return out
}

func wordBounds(words []layoutWord) (int, int, int, int) {
	x, y, r, b := words[0].x, words[0].y, 0, 0
	for _, w := range words {
		x = min(x, w.x)
		y = min(y, w.y)
		r = max(r, w.x+w.w)
		b = max(b, w.y+w.h)
	}
	return x, y, r, b
}

func spatialBlocks(words []layoutWord, height, depth int) []TextBlock {
	// A shared, wide vertical gutter with prose on both sides is a column
	// boundary. Numeric amounts remain with their row in tables.
	if depth < 6 {
		byX := append([]layoutWord{}, words...)
		sort.Slice(byX, func(i, j int) bool { return byX[i].x < byX[j].x })
		right := byX[0].x + byX[0].w
		best, cut := 0, 0
		for _, w := range byX[1:] {
			gap := w.x - right
			if gap > best && gap > height*2 {
				leftWords, rightWords := splitWords(words, right+gap/2)
				if proseLines(leftWords) >= 3 && proseLines(rightWords) >= 3 {
					best, cut = gap, right+gap/2
				}
			}
			right = max(right, w.x+w.w)
		}
		if best > 0 {
			left, right := splitWords(words, cut)
			return []TextBlock{{Kind: "columns", Columns: [][]TextBlock{spatialBlocks(left, height, depth+1), spatialBlocks(right, height, depth+1)}}}
		}
	}
	return linesToBlocks(groupLines(words), height)
}
func splitWords(words []layoutWord, cut int) ([]layoutWord, []layoutWord) {
	var left, right []layoutWord
	for _, w := range words {
		if w.x < cut {
			left = append(left, w)
		} else {
			right = append(right, w)
		}
	}
	return left, right
}
func proseLines(words []layoutWord) int {
	lines := map[[3]int]int{}
	for _, w := range words {
		for _, r := range w.text {
			if unicode.IsLetter(r) {
				lines[[3]int{w.block, w.par, w.line}]++
			}
		}
	}
	n := 0
	for _, letters := range lines {
		if letters >= 6 {
			n++
		}
	}
	return n
}
func groupLines(words []layoutWord) []layoutLine {
	grouped := map[[3]int][]layoutWord{}
	for _, w := range words {
		key := [3]int{w.block, w.par, w.line}
		grouped[key] = append(grouped[key], w)
	}
	lines := []layoutLine{}
	for _, ws := range grouped {
		sort.SliceStable(ws, func(i, j int) bool { return ws[i].x < ws[j].x })
		lines = append(lines, layoutLine{words: ws, par: ws[0].par})
	}
	sort.SliceStable(lines, func(i, j int) bool {
		_, yi, _, _ := wordBounds(lines[i].words)
		_, yj, _, _ := wordBounds(lines[j].words)
		return yi < yj
	})
	return lines
}
func lineText(words []layoutWord) string {
	values := make([]string, len(words))
	for i, w := range words {
		values[i] = w.text
	}
	return strings.Join(values, " ")
}
func lineCells(words []layoutWord, height int) [][]layoutWord {
	hs := make([]int, len(words))
	for i, w := range words {
		hs[i] = w.h
	}
	sort.Ints(hs)
	height = max(height, hs[len(hs)/2])
	cells := [][]layoutWord{{words[0]}}
	for i, w := range words[1:] {
		previous := words[i]
		// Normal spaces are much narrower than a word's height in proportional
		// text. A larger gap retains a distinct cell, including label/value rows.
		if w.x-(previous.x+previous.w) > height*3/2 {
			cells = append(cells, []layoutWord{})
		}
		cells[len(cells)-1] = append(cells[len(cells)-1], w)
	}
	// A recognized border glyph is retained with its neighboring cell instead
	// of consuming a whole extra column in a letter's metadata table.
	for i := 0; i < len(cells) && len(cells) > 1; {
		if strings.IndexFunc(lineText(cells[i]), func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
			i++
			continue
		}
		if i == 0 {
			cells[1] = append(cells[0], cells[1]...)
			cells = cells[1:]
		} else {
			cells[i-1] = append(cells[i-1], cells[i]...)
			cells = append(cells[:i], cells[i+1:]...)
		}
	}
	return cells
}
func headingLine(words []layoutWord, height int) bool {
	text := lineText(words)
	if len([]rune(text)) > 85 {
		return false
	}
	hs := []int{}
	letters, upper := 0, 0
	for _, w := range words {
		hs = append(hs, w.h)
		for _, r := range w.text {
			if unicode.IsLetter(r) {
				letters++
				if unicode.IsUpper(r) {
					upper++
				}
			}
		}
	}
	sort.Ints(hs)
	return (letters >= 3 && hs[len(hs)/2] > height*3/2) || (letters >= 5 && upper == letters)
}
func linesToBlocks(lines []layoutLine, height int) []TextBlock {
	out := []TextBlock{}
	for i := 0; i < len(lines); {
		// Collect a run with repeated cells; single isolated large word spacing
		// does not imply a table. Sparse continuation lines keep empty cells.
		j, multi, maxCells := i, 0, 0
		for ; j < len(lines); j++ {
			if j > i && lines[j].par != lines[i].par {
				break
			}
			cells := lineCells(lines[j].words, height)
			maxCells = max(maxCells, len(cells))
			if len(cells) > 1 {
				multi++
			} else if j == i || lines[j].words[0].x <= lines[i].words[0].x+height {
				break
			}
		}
		if multi >= 2 || maxCells >= 3 {
			out = append(out, tableBlock(lines[i:j], height))
			i = j
			continue
		}
		ws := lines[i].words
		text := lineText(ws)
		if headingLine(ws, height) {
			out = append(out, TextBlock{Kind: "heading", Text: text})
			i++
			continue
		}
		kind := "paragraph"
		if len(lineCells(ws, height)) > 1 {
			kind = "pre"
			text = spacedLine(ws, height)
		}
		if len(out) > 0 && out[len(out)-1].Kind == kind && i > 0 && lines[i].par == lines[i-1].par {
			out[len(out)-1].Text += "\n" + text
		} else {
			out = append(out, TextBlock{Kind: kind, Text: text})
		}
		i++
	}
	return out
}
func spacedLine(words []layoutWord, height int) string {
	var b strings.Builder
	right := words[0].x
	for _, w := range words {
		if b.Len() > 0 {
			b.WriteString(strings.Repeat(" ", min(60, max(1, (w.x-right)/max(1, height/2)))))
		}
		b.WriteString(w.text)
		right = w.x + w.w
	}
	return b.String()
}
func tableBlock(lines []layoutLine, height int) TextBlock {
	// Use stable starts of the fullest row as column anchors. Assign individual
	// words so a narrow gap between a long label and its value still aligns.
	var anchors []int
	for _, line := range lines {
		cells := lineCells(line.words, height)
		if len(cells) > len(anchors) {
			anchors = nil
			for _, cell := range cells {
				x := cell[0].x
				for _, w := range cell {
					if strings.IndexFunc(w.text, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
						x = w.x
						break
					}
				}
				anchors = append(anchors, x)
			}
		}
	}
	rows := [][]string{}
	for _, line := range lines {
		row := make([]string, len(anchors))
		for _, w := range line.words {
			index := 0
			for i, x := range anchors {
				if w.x+height/2 >= x {
					index = i
				}
			}
			if row[index] != "" {
				row[index] += " "
			}
			row[index] += w.text
		}
		rows = append(rows, row)
	}
	return TextBlock{Kind: "table", Rows: rows}
}

func markdownText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\\", "\\\\", "|", "\\|", "*", "\\*", "_", "\\_", "`", "\\`", "[", "\\[", "]", "\\]", "#", "\\#").Replace(s)
}
func blocksText(blocks []TextBlock, markdown bool) string {
	var parts []string
	for _, b := range blocks {
		switch b.Kind {
		case "columns":
			for _, column := range b.Columns {
				parts = append(parts, blocksText(column, markdown))
			}
		case "table":
			if markdown {
				// Empty headings avoid pretending a data row is a semantic header.
				n := len(b.Rows[0])
				parts = append(parts, "|"+strings.Repeat(" |", n)+"\n|"+strings.Repeat(" --- |", n))
				for _, row := range b.Rows {
					cells := make([]string, len(row))
					for i, c := range row {
						cells[i] = strings.ReplaceAll(markdownText(c), "\n", "<br>")
					}
					parts[len(parts)-1] += "\n| " + strings.Join(cells, " | ") + " |"
				}
			} else {
				for _, row := range b.Rows {
					parts = append(parts, strings.Join(row, "\t"))
				}
			}
		case "heading":
			if markdown {
				parts = append(parts, "## "+markdownText(b.Text))
			} else {
				parts = append(parts, b.Text)
			}
		case "pre":
			if markdown {
				parts = append(parts, "    "+strings.ReplaceAll(b.Text, "\n", "\n    "))
			} else {
				parts = append(parts, b.Text)
			}
		default:
			if markdown {
				parts = append(parts, strings.ReplaceAll(markdownText(b.Text), "\n", "  \n"))
			} else {
				parts = append(parts, b.Text)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}
