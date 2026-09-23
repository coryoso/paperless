package classify

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"paperless/internal/config"
	"paperless/internal/document"
	"strings"
	"testing"
)

func sampleBlocks(n int) []OCRBlock {
	blocks := make([]OCRBlock, n)
	for i := range blocks {
		blocks[i].ID = i + 1
		blocks[i].Page = i/8 + 1
		blocks[i].Content = fmt.Sprintf("Ordinary document paragraph %d", i+1)
		blocks[i].Position = &document.Position{}
		blocks[i].Size = &document.Size{Width: 120, Height: 30}
	}
	return blocks
}

func blockTestServer(t *testing.T, completion func(map[string]json.RawMessage, map[string]any) string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			fmt.Fprint(w, `{"default_generation_settings":{"n_ctx":65536}}`)
			return
		case "/tokenize":
			fmt.Fprint(w, `{"tokens":[1,2,3]}`)
			return
		case "/v1/chat/completions":
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var req struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			ResponseFormat struct {
				Type   string `json:"type"`
				Schema struct {
					Strict bool           `json:"strict"`
					Schema map[string]any `json:"schema"`
				} `json:"json_schema"`
			} `json:"response_format"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if req.ResponseFormat.Type != "json_schema" || !req.ResponseFormat.Schema.Strict {
			t.Error("schema not enforced")
		}
		for _, key := range []string{`"position"`, `"size"`, `"page"`, `"table_candidate"`} {
			if strings.Contains(req.Messages[1].Content, key) {
				t.Error("extraneous model input", key)
			}
		}
		var prompt map[string]json.RawMessage
		if err := json.Unmarshal([]byte(req.Messages[1].Content), &prompt); err != nil {
			t.Error(err)
			return
		}
		output := completion(prompt, req.ResponseFormat.Schema.Schema)
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": output}, "finish_reason": "stop"}}})
	}))
}

func TestBlockLabelsUseContentOnlyAndMapIDs(t *testing.T) {
	blocks := sampleBlocks(4)
	for i := range blocks {
		blocks[i].ID = (i + 1) * 10
	}
	calls := 0
	server := blockTestServer(t, func(prompt map[string]json.RawMessage, _ map[string]any) string {
		calls++
		if len(prompt) != 1 {
			t.Error("unexpected context fields")
		}
		var target map[string]any
		json.Unmarshal(prompt["target_block"], &target)
		if len(target) != 2 || target["id"] != float64(calls*10) || target["content"] != blocks[calls-1].Content {
			t.Error("model input must contain only the original ID and content")
		}
		return `{"type":"sender_name_or_address"}`
	})
	defer server.Close()
	cfg := config.Default()
	cfg.Bonsai.Endpoint = server.URL
	labels, err := LabelOCRBlocks(t.Context(), cfg, blocks)
	if err != nil || len(labels) != 4 || calls != 4 {
		t.Fatalf("labels=%v calls=%d err=%v", labels, calls, err)
	}
	for i, label := range labels {
		if label.ID != blocks[i].ID || label.Type != "sender" || label.Representation != "paragraph" {
			t.Fatal(label)
		}
	}
}

func TestShortTextUsesSamePageNeighborsOnly(t *testing.T) {
	blocks := sampleBlocks(5)
	blocks[0].Page = 1
	for i := 1; i < len(blocks); i++ {
		blocks[i].Page = 2
	}
	calls := 0
	server := blockTestServer(t, func(prompt map[string]json.RawMessage, _ map[string]any) string {
		calls++
		var target blockContent
		json.Unmarshal(prompt["target_block"], &target)
		if target.ID != 3 {
			return `{"type":"sender_name_or_address"}`
		}
		if _, ok := prompt["surrounding_blocks"]; !ok {
			return `{"type":"body_text"}`
		}
		var neighbors []blockContent
		json.Unmarshal(prompt["surrounding_blocks"], &neighbors)
		if len(neighbors) != 3 || neighbors[0].ID != 2 || neighbors[1].ID != 4 || neighbors[2].ID != 5 {
			t.Error("wrong neighbor window", neighbors)
		}
		return `{"type":"recipient_name_or_address"}`
	})
	defer server.Close()
	cfg := config.Default()
	cfg.Bonsai.Endpoint = server.URL
	labels, err := LabelOCRBlocks(t.Context(), cfg, blocks)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 6 || labels[2].Type != "recipient" {
		t.Fatal(labels, calls)
	}
}

func TestTableRepresentationUsesContentWithoutGeometry(t *testing.T) {
	blocks := sampleBlocks(1)
	blocks[0].TableCandidate = true
	server := blockTestServer(t, func(_ map[string]json.RawMessage, schema map[string]any) string {
		properties := schema["properties"].(map[string]any)
		if properties["representation"] == nil {
			t.Error("missing representation schema")
		}
		return `{"type":"body_text","representation":"table"}`
	})
	defer server.Close()
	cfg := config.Default()
	cfg.Bonsai.Endpoint = server.URL
	labels, err := LabelOCRBlocks(t.Context(), cfg, blocks)
	if err != nil {
		t.Fatal(err)
	}
	if labels[0].Representation != "table" {
		t.Fatal(labels)
	}
}

func TestBlockLabelsRejectMalformedResponses(t *testing.T) {
	for _, output := range []string{
		`{}`, `{"type":"invoice"}`, `{"type":"body_text","representation":"paragraph"}`,
		`{"type":"body_text","id":99}`, `{"type":"body_text"} {"type":"document_date"}`, `not json`,
	} {
		t.Run(output, func(t *testing.T) {
			server := blockTestServer(t, func(map[string]json.RawMessage, map[string]any) string { return output })
			defer server.Close()
			cfg := config.Default()
			cfg.Bonsai.Endpoint = server.URL
			if labels, err := LabelOCRBlocks(t.Context(), cfg, sampleBlocks(1)); err == nil || labels != nil {
				t.Fatal("accepted bad label", labels, err)
			}
		})
	}
}

func TestBlockInputValidation(t *testing.T) {
	for _, blocks := range [][]OCRBlock{nil, sampleBlocks(257), {{ID: 1, Page: 1, Content: "incomplete geometry", Position: &document.Position{}}}, append(sampleBlocks(1), sampleBlocks(1)...)} {
		if ValidateOCRBlocks(blocks) == nil {
			t.Fatal("accepted invalid blocks")
		}
	}
	blocks := sampleBlocks(1)
	blocks[0].Content = strings.Repeat("x", 48001)
	if err := ValidateOCRBlocks(blocks); err != nil {
		t.Fatal("text size should use the actual token window, not an arbitrary 48 KB limit", err)
	}
}
func TestBonsaiLiveBlockLabels(t *testing.T) {
	endpoint := os.Getenv("PAPERLESS_BONSAI_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("set PAPERLESS_BONSAI_TEST_ENDPOINT for live block classification")
	}
	cfg := config.Default()
	cfg.Bonsai.Endpoint = endpoint
	blocks := sampleBlocks(4)
	for i, content := range []string{"Northstar Library\nLibrary Street 1\n12345 Berlin", "To: Alex Example\nExample Street 2\n12345 Berlin", "Invitation to our summer reading event", "Join us for an afternoon of reading and discussion. Admission is free."} {
		blocks[i].Content = content
		blocks[i].Page = 1
		blocks[i].Position.Y = float64(i * 100)
	}
	labels, err := LabelOCRBlocks(t.Context(), cfg, blocks)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"sender", "recipient", "subject", "text"} {
		if labels[i].Type != want || labels[i].Representation != "paragraph" {
			t.Errorf("block %d: %+v, want %s paragraph", i+1, labels[i], want)
		}
	}
}

func TestBonsaiLiveGermanBlockLabels(t *testing.T) {
	endpoint := os.Getenv("PAPERLESS_BONSAI_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("set PAPERLESS_BONSAI_TEST_ENDPOINT")
	}
	cfg := config.Default()
	cfg.Bonsai.Endpoint = endpoint
	blocks := sampleBlocks(8)
	for i, content := range []string{
		"Stadtbibliothek Beispielstadt", "Bibliotheksweg 1\n12345 Beispielstadt",
		"Herrn Alex Beispiel\nMusterweg 2\n12345 Beispielstadt", "Datum: 20.09.2026",
		"Einladung zur Lesung", "Kundennummer: 123456",
		"Wir laden Sie herzlich zur Lesung am Samstag ein.", "Der Eintritt ist frei. Wir freuen uns auf Ihren Besuch.",
	} {
		blocks[i].Content = content
		blocks[i].Page = 1
		blocks[i].Position.Y = float64(i * 100)
	}
	labels, err := LabelOCRBlocks(t.Context(), cfg, blocks)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"sender", "", "recipient", "date", "subject", "reference", "text", "text"} {
		if want == "" {
			continue
		} // A detached address line can have an ambiguous role.
		if labels[i].Type != want {
			t.Errorf("block %d: %s, want %s", i+1, labels[i].Type, want)
		}
	}
}

// Synthetic notice exercises a heading plus subtitle and a detached date.
func TestBonsaiLiveNoticeBlockLabels(t *testing.T) {
	endpoint := os.Getenv("PAPERLESS_BONSAI_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("set PAPERLESS_BONSAI_TEST_ENDPOINT")
	}
	cfg := config.Default()
	cfg.Bonsai.Endpoint = endpoint
	cfg.LLM.ContextTokens = 65536
	blocks := sampleBlocks(4)
	for i, content := range []string{
		"Polizei Beispielstadt\nBußgeldstelle",
		"Herrn\nALEX BEISPIEL\nMUSTERWEG 12\n12345 BEISPIELSTADT",
		"20.08.2026",
		"Schriftliche Verwarnung mit Verwarnungsgeld / Anhörung\nVerwarnungen werden nicht im Fahreignungsregister eingetragen.",
	} {
		blocks[i].Content = content
	}
	labels, err := LabelOCRBlocks(t.Context(), cfg, blocks)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []string{"sender", "recipient", "date", "subject"} {
		if labels[i].Type != want {
			t.Errorf("block %d: %s, want %s", i+1, labels[i].Type, want)
		}
	}
}

func TestOrdinarySentencesDoNotInheritNeighborRoles(t *testing.T) {
	blocks := sampleBlocks(3)
	for i, content := range []string{"Wir laden Sie herzlich ein.", "Welcome!", "Can you join us?"} {
		blocks[i].Content = content
	}
	calls := 0
	server := blockTestServer(t, func(prompt map[string]json.RawMessage, _ map[string]any) string {
		calls++
		if _, ok := prompt["surrounding_blocks"]; ok {
			t.Error("ordinary sentence should keep its independent role")
		}
		return `{"type":"body_text"}`
	})
	defer server.Close()
	cfg := config.Default()
	cfg.Bonsai.Endpoint = server.URL
	labels, err := LabelOCRBlocks(t.Context(), cfg, blocks)
	if err != nil || calls != 3 || len(labels) != 3 {
		t.Fatal(labels, calls, err)
	}
}
