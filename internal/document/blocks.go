// Package document defines the persisted, spatially grouped document text.
package document

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"strings"
)

const Version = 1

type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
type Size struct {
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}
type Block struct {
	ID             int       `json:"id"`
	Type           string    `json:"type"`
	Representation string    `json:"representation"`
	Page           int       `json:"page"`
	Position       *Position `json:"position"`
	Size           *Size     `json:"size"`
	Content        string    `json:"content"`
	TableCandidate bool      `json:"table_candidate,omitempty"`
}
type Document struct {
	Unified            *Unified `json:"unified,omitempty"`
	Version            int      `json:"version"`
	SourceHash         string   `json:"source_hash"`
	DistanceMultiplier float64  `json:"distance_multiplier"`
	Blocks             []Block  `json:"blocks"`
}

func Hash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func (d Document) Text() string {
	parts := make([]string, 0, len(d.Blocks))
	for _, b := range d.Blocks {
		parts = append(parts, b.Content)
	}
	return strings.Join(parts, "\n\n")
}

// ModelJSON preserves semantic blocks without spending tokens on coordinates.
func (d Document) ModelJSON() string {
	type textBlock struct {
		ID      int    `json:"id"`
		Type    string `json:"type"`
		Content string `json:"content"`
	}
	blocks := make([]textBlock, 0, len(d.Blocks))
	for _, b := range d.Blocks {
		blocks = append(blocks, textBlock{b.ID, b.Type, b.Content})
	}
	data, _ := json.Marshal(blocks)
	return string(data)
}

type Word struct {
	X, Y, W, H float64
	Text       string
}

func wordLess(a, b Word) bool {
	if a.Y != b.Y {
		return a.Y < b.Y
	}
	if a.X != b.X {
		return a.X < b.X
	}
	return a.Text < b.Text
}

// Cluster mirrors the preview: connected components of word rectangles, with a
// median-height dynamic distance. Original rectangles, never growing bounds, join.
func Cluster(words []Word, page int, multiplier float64) []Block {
	valid := make([]Word, 0, len(words))
	heights := []float64{}
	for _, w := range words {
		if strings.TrimSpace(w.Text) != "" && w.X >= 0 && w.Y >= 0 && w.W > 0 && w.H > 0 && !math.IsInf(w.X+w.Y+w.W+w.H, 0) {
			valid = append(valid, w)
			heights = append(heights, w.H)
		}
	}
	if len(valid) == 0 {
		return nil
	}
	words = valid
	sort.SliceStable(words, func(i, j int) bool { return wordLess(words[i], words[j]) })
	sort.Float64s(heights)
	height := (heights[len(heights)/2] + heights[(len(heights)-1)/2]) / 2
	distance := height * multiplier
	parents := make([]int, len(words))
	for i := range parents {
		parents[i] = i
	}
	var root func(int) int
	root = func(i int) int {
		for parents[i] != i {
			parents[i] = parents[parents[i]]
			i = parents[i]
		}
		return i
	}
	for i, a := range words {
		for j := i + 1; j < len(words); j++ {
			b := words[j]
			if b.Y > a.Y+a.H+distance {
				break
			}
			dx := math.Max(0, math.Max(a.X-b.X-b.W, b.X-a.X-a.W))
			dy := math.Max(0, math.Max(a.Y-b.Y-b.H, b.Y-a.Y-a.H))
			if dx*dx+dy*dy <= distance*distance {
				parents[root(j)] = root(i)
			}
		}
	}
	groups := map[int][]Word{}
	order := []int{}
	for i, w := range words {
		key := root(i)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], w)
	}
	blocks := make([]Block, 0, len(groups))
	for _, key := range order {
		group := groups[key]
		left, top, right, bottom := group[0].X, group[0].Y, 0.0, 0.0
		for _, w := range group {
			left = math.Min(left, w.X)
			top = math.Min(top, w.Y)
			right = math.Max(right, w.X+w.W)
			bottom = math.Max(bottom, w.Y+w.H)
		}
		lines := orderedLines(group)
		text := []string{}
		rows := [][]float64{}
		for _, line := range lines {
			content := []string{}
			starts := []float64{line[0].X}
			for i, w := range line {
				content = append(content, w.Text)
				if i > 0 && w.X-line[i-1].X-line[i-1].W > height*1.5 {
					starts = append(starts, w.X)
				}
			}
			text = append(text, strings.Join(content, " "))
			if len(starts) > 1 {
				rows = append(rows, starts)
			}
		}
		table := false
		for i, row := range rows {
			for _, other := range rows[i+1:] {
				if len(row) != len(other) {
					continue
				}
				aligned := true
				for j, x := range row {
					if math.Abs(x-other[j]) > height/2 {
						aligned = false
					}
				}
				table = table || aligned
			}
		}
		blocks = append(blocks, Block{Page: page, Position: &Position{left, top}, Size: &Size{right - left, bottom - top}, Content: strings.Join(text, "\n"), TableCandidate: table})
	}
	sort.SliceStable(blocks, func(i, j int) bool {
		a, b := blocks[i], blocks[j]
		if a.Position.Y != b.Position.Y {
			return a.Position.Y < b.Position.Y
		}
		return a.Position.X < b.Position.X
	})
	return blocks
}
func orderedLines(words []Word) [][]Word {
	type line struct {
		center, height float64
		words          []Word
	}
	lines := []line{}
	for _, word := range words {
		center := word.Y + word.H/2
		found := false
		for i := range lines {
			if math.Abs(lines[i].center-center) <= math.Min(lines[i].height, word.H)/2 {
				lines[i].words = append(lines[i].words, word)
				found = true
				break
			}
		}
		if !found {
			lines = append(lines, line{center, word.H, []Word{word}})
		}
	}
	out := [][]Word{}
	for _, line := range lines {
		sort.SliceStable(line.words, func(i, j int) bool {
			a, b := line.words[i], line.words[j]
			if a.X != b.X {
				return a.X < b.X
			}
			return wordLess(a, b)
		})
		out = append(out, line.words)
	}
	return out
}
