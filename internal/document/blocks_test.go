package document

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDynamicProximityAndReadingOrder(t *testing.T) {
	words := []Word{{0, 0, 40, 10, "Hello"}, {45, 0, 40, 10, "world"}, {0, 15, 40, 10, "second"}, {45, 15, 40, 10, "line"}, {150, 0, 50, 10, "Separate"}}
	blocks := Cluster(words, 1, 1)
	if len(blocks) != 2 || blocks[0].Content != "Hello world\nsecond line" || blocks[0].Size.Width != 85 || blocks[0].Size.Height != 25 {
		t.Fatal(blocks)
	}
	for i := range words {
		words[i].X *= 3
		words[i].Y *= 3
		words[i].W *= 3
		words[i].H *= 3
	}
	scaled := Cluster(words, 1, 1)
	if len(scaled) != 2 || scaled[0].Content != blocks[0].Content {
		t.Fatal(scaled)
	}
	if got := Cluster(words, 1, 0); len(got) != 5 {
		t.Fatal("zero distance merged words", got)
	}
}
func TestModelProjectionExcludesGeometry(t *testing.T) {
	doc := Document{Blocks: []Block{{ID: 4, Page: 2, Type: "subject", Content: "Invitation", Position: &Position{12, 30}, Size: &Size{100, 20}, TableCandidate: true}}}
	prompt := doc.ModelJSON()
	if !json.Valid([]byte(prompt)) || !strings.Contains(prompt, "Invitation") {
		t.Fatal(prompt)
	}
	for _, field := range []string{"position", "size", "table_candidate", "page"} {
		if strings.Contains(prompt, field) {
			t.Fatal(prompt)
		}
	}
}
