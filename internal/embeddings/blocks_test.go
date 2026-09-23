package embeddings

import (
	"paperless/internal/document"
	"strings"
	"testing"
)

func TestJSONBlockEmbeddingsKeepContentAndSourceReferences(t *testing.T) {
	doc := document.Document{Blocks: []document.Block{{ID: 5, Type: "sender", Content: "Library", Position: &document.Position{X: 123, Y: 456}}, {ID: 9, Type: "text", Content: strings.Repeat("z", 2600)}}}
	chunks, ids, err := DocumentChunks(doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 4 || len(ids) != 4 || chunks[0] != "Library" || ids[0] != 5 {
		t.Fatal(chunks, ids)
	}
	for i := 1; i < len(chunks); i++ {
		if ids[i] != 9 || strings.Trim(chunks[i], "z") != "" || len(chunks[i]) > 1200 {
			t.Fatal(chunks[i], ids[i])
		}
	}
}

func TestUnifiedEmbeddingsRemoveDuplicateMetadataAndKeepAllSources(t *testing.T) {
	doc := document.Document{Blocks: []document.Block{{ID: 1, Type: "sender", Content: "Library"}, {ID: 2, Type: "sender", Content: "City Library"}, {ID: 3, Type: "text", Content: "Invitation to the reading"}}}
	doc.Unified = document.Consolidate(doc.Blocks)
	chunks, sources, err := UnifiedChunks(doc)
	if err != nil || len(chunks) != 2 || chunks[0] != "City Library" || len(sources[0]) != 2 || sources[0][0] != 1 || sources[0][1] != 2 {
		t.Fatal(chunks, sources, err)
	}
}
