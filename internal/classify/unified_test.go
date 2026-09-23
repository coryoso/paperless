package classify

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"paperless/internal/config"
	"paperless/internal/document"
	"strings"
	"testing"
	"time"
)

func TestGroundedUnifiedValues(t *testing.T) {
	for _, tc := range []struct {
		source, value string
		want          bool
	}{
		{"Ihre Renteninformation", "Your pension information", false},
		{"E-Mail beitrag@drv-\nberlin-brandenburg.de", "beitrag@drv-berlin-brandenburg.de", true},
		{"Deutsche Rentenversicherung\nBerlin-Brandenburg", "Deutsche Rentenversicherung Berlin-Brandenburg", true},
		{"Polizei Berlin", "Polizei Hamburg", false},
		{"ABC 123", "", false},
	} {
		if got := groundedValue(tc.source, tc.value); got != tc.want {
			t.Errorf("%q in %q = %v", tc.value, tc.source, got)
		}
	}
}
func TestUnifiedFilenameUsesOriginalSubjectAndKeepsRouting(t *testing.T) {
	c := Classification{DocumentDate: "2026-08-11", Sender: "old", Recipient: "saved-recipient", RecipientProfileID: 9, SuggestedFolder: "Personal", Summary: "English summary"}
	ApplyUnified(&c, &document.Unified{Subject: "Ihre Renteninformation", Sender: document.Party{Names: []string{"Deutsche Rentenversicherung"}}, Recipient: document.Party{Names: []string{"Alex Example"}}, ExtractionStatus: "complete"})
	if c.SuggestedFilename != "2026-08-11__deutsche-rentenversicherung__ihre-renteninformation.pdf" || c.RecipientProfileID != 9 || c.Recipient != "saved-recipient" || c.SuggestedFolder != "Personal" {
		t.Fatal(c)
	}
}

func TestMergedFieldsKeepDistinctValuesAndFragmentSources(t *testing.T) {
	values := deduplicateValues([]string{"City Library", "City Library North", "Community Library"})
	if len(values) != 2 || values[0] != "City Library North" || values[1] != "Community Library" {
		t.Fatal(values)
	}
	blocks := []document.Block{{ID: 1, Type: "sender", Page: 1, Content: "City Library"}, {ID: 2, Type: "reference", Page: 1, Content: "ABC"}, {ID: 3, Type: "sender", Page: 1, Content: "North"}}
	ids := valueSources(blocks, "sender", "City Library North")
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 3 {
		t.Fatal(ids)
	}
	blocks[2].Page = 2
	if ids = valueSources(blocks, "sender", "City Library North"); len(ids) != 0 {
		t.Fatal("cross-page source concatenation", ids)
	}
}

func TestDocumentDateSupportsWrittenMonthsAndFilenameLength(t *testing.T) {
	date, reason := extractDate("11. August 2026", time.Time{})
	if date != "2026-08-11" || reason != "document date found" {
		t.Fatal(date, reason)
	}
	name := BuildFilename(date, strings.Repeat("Long name ", 100), strings.Repeat("Überprüfung ", 100))
	if len(name) > 220 || strings.Count(name, "__") != 2 || strings.Contains(name, "__letter__") {
		t.Fatal(name)
	}
}

func TestStructuredUnificationRejectsInventedValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/props":
			fmt.Fprint(w, `{"default_generation_settings":{"n_ctx":65536}}`)
		case "/tokenize":
			fmt.Fprint(w, `{"tokens":[1,2]}`)
		default:
			var input struct {
				ResponseFormat struct {
					Type       string `json:"type"`
					JSONSchema struct {
						Schema struct {
							Properties map[string]any `json:"properties"`
						} `json:"schema"`
					} `json:"json_schema"`
				} `json:"response_format"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input.ResponseFormat.Type != "json_schema" {
				t.Error("missing constrained schema")
			}
			content := `{"names":["City Library","Invented Institution"],"addresses":[],"phones":[],"faxes":[],"emails":[],"websites":[]}`
			if _, ok := input.ResponseFormat.JSONSchema.Schema.Properties["subject"]; ok {
				content = `{"subject":"Einladung zum Lesetag"}`
			}
			json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": content}}}})
		}
	}))
	defer server.Close()
	cfg := config.Default()
	cfg.LLM.Enabled = true
	cfg.LLM.Provider = "bonsai"
	cfg.Bonsai.Endpoint = server.URL
	cfg.LLM.ContextTokens = 65536
	doc := document.Document{Blocks: []document.Block{{ID: 4, Type: "sender", Content: "City Library"}, {ID: 7, Type: "subject", Content: "Einladung zum Lesetag"}}}
	u, err := UnifyDocument(t.Context(), cfg, doc)
	if err != nil || u.ExtractionStatus != "partial" || len(u.Sender.Names) != 1 || u.Sender.Names[0] != "City Library" || u.Subject != "Einladung zum Lesetag" || u.SubjectSourceIDs[0] != 7 {
		t.Fatal(u, err)
	}
}
