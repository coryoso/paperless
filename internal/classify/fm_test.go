package classify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"paperless/internal/config"
	"paperless/internal/progress"
)

const fmTestResponse = `{"document_type":"tax-letter","sender":"Finanzamt","recipient":"Alex Example","recipient_type":"person","recipient_scope":"personal","recipient_evidence":"Herrn Alex Example","document_date":"2026-02-25","summary":"Tax notice","suggested_folder":"Tax/2026","confidence":0.95,"reasons":["Personal tax notice"]}`

func installFakeFM(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
export LC_ALL=C
input=$(cat)
case "$1" in
count-tokens) instructions="$4"; echo $(((${#input} + ${#instructions} + 3) / 4));;
respond)
  printf '%s' "$input" > "$FM_TEST_PROMPT"
  printf '%s' "$FM_TEST_RESPONSE"
  ;;
*) exit 1;;
esac
`
	if err := os.WriteFile(filepath.Join(dir, "fm"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	promptFile := filepath.Join(dir, "prompt")
	t.Setenv("FM_TEST_PROMPT", promptFile)
	t.Setenv("FM_TEST_RESPONSE", fmTestResponse)
	return promptFile
}

func TestFMClassificationAndPolicyMerge(t *testing.T) {
	promptFile := installFakeFM(t)
	cfg := config.Default()
	cfg.LLM.Provider = "fm"
	cfg.RecipientProfiles = []config.RecipientProfile{{Name: "Alex Example", Scope: "personal", Aliases: []string{"A. Example"}, FolderPrefix: "Tax"}}
	text := "Finanzamt\nHerrn Alex Example\n25.02.2026\nEinkommensteuerbescheid"
	var events []progress.Event
	result := ClassifyWithHistory(t.Context(), cfg, text, "scan.pdf", time.Now(), []string{"Tax/2026"}, nil, func(event progress.Event) { events = append(events, event) }, "# Letter\n"+text)
	if result.Source != "fm" || result.PhysicalOriginalAction != "review" || result.RecipientScope != "personal" || result.SuggestedFolder != "Tax/2026" {
		t.Fatal(result)
	}
	if !strings.HasPrefix(result.SuggestedFilename, "2026-02-25__finanzamt__document.pdf") {
		t.Fatalf("filename = %s", result.SuggestedFilename)
	}
	prompt, _ := os.ReadFile(promptFile)
	if !strings.Contains(string(prompt), "# Letter") || !strings.Contains(string(prompt), "A. Example") {
		t.Fatalf("missing layout or profiles: %s", prompt)
	}
	for _, event := range events {
		if strings.Contains(strings.ToLower(event.Message), "ollama") {
			t.Fatalf("wrong provider in progress: %s", event.Message)
		}
	}
}

func TestFMInvalidResponseFallsBackToRules(t *testing.T) {
	installFakeFM(t)
	for _, output := range []string{"not JSON", "{}", "null", `{"document_type":"made-up"}`} {
		t.Setenv("FM_TEST_RESPONSE", output)
		cfg := config.Default()
		cfg.LLM.Provider = "fm"
		result := Classify(t.Context(), cfg, "REWE Kassenbon Gesamt EUR 12", "scan.pdf", time.Now(), []string{"Belege"})
		if result.Source != "rules" || !strings.Contains(strings.Join(result.Reasons, " "), "fm unavailable or invalid") {
			t.Fatalf("fallback = %+v", result)
		}
	}
}

func TestFMContextBudgetKeepsValidJSONAndMarksReview(t *testing.T) {
	installFakeFM(t)
	input := fmInput{Document: strings.Repeat("Grüße 漢字 invoice ", 4000), Folders: []string{"Tax/2026"}}
	prompt, schema, truncated, err := prepareFMInput(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || !json.Valid([]byte(prompt)) || !utf8.ValidString(prompt) || !json.Valid(schema) {
		t.Fatalf("invalid bounded input: truncated=%v", truncated)
	}
	if len(prompt)+len(schema)+len(fmInstructions) > fmInputTokens*4+4 {
		t.Fatal("context exceeds token budget")
	}
	cfg := config.Default()
	cfg.LLM.Provider = "fm"
	result := Classify(t.Context(), cfg, input.Document, "long.pdf", time.Now(), input.Folders)
	if result.Source != "fm" || !result.ModelContextTruncated {
		t.Fatalf("truncation marker lost in merge: %+v", result)
	}
	t.Setenv("FM_TEST_RESPONSE", "invalid")
	result = Classify(t.Context(), cfg, input.Document, "long.pdf", time.Now(), input.Folders)
	if result.Source != "rules" || !result.ModelContextTruncated {
		t.Fatalf("truncation marker lost in fallback: %+v", result)
	}
}

// Opt in explicitly so CI never requires Apple Intelligence or invokes a real
// local model. This uses a synthetic receipt, not a user's private document.
func TestFMIntegration(t *testing.T) {
	if os.Getenv("PAPERLESS_TEST_FM") != "1" {
		t.Skip("set PAPERLESS_TEST_FM=1 to test the installed fm command")
	}
	cfg := config.Default()
	cfg.LLM.Provider = "fm"
	result := Classify(t.Context(), cfg, "REWE Markt\nKassenbon\n18.09.2026\nGesamt EUR 12,34\nMwSt 7%", "receipt.pdf", time.Now(), []string{"Belege"})
	if result.Source != "fm" || result.SuggestedFolder != "Belege" {
		t.Fatal(result)
	}
	t.Logf("fm classified synthetic receipt: %s, %s", result.Source, result.SuggestedFilename)
}
