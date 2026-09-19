package classify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"paperless/internal/bonsai"
	"paperless/internal/config"
	"paperless/internal/progress"
)

const bonsaiInstructions = fmInstructions + ` A request for payment with an invoice number and a payment due date is a routine-invoice. A receipt records a completed purchase or payment; a VAT breakdown alone does not establish that payment was completed.`

func classifyWithBonsai(ctx context.Context, cfg config.Config, text, filename string, date time.Time, folders []string, reporter progress.Reporter, examples []RoutingExample) (Classification, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.LLM.TimeoutSeconds)*time.Second)
	defer cancel()
	reporter.Info("llm", "model", "Using Bonsai model "+cfg.Bonsai.Model+".", 0, 0, 90)
	// Bound document input as with Ollama, and require review whenever text was
	// shortened. Rune slicing preserves UTF-8 in multilingual documents.
	runes := []rune(text)
	truncated := len(runes) > 12_000
	if truncated {
		text = string(runes[:12_000])
		reporter.Warn("llm", "context", "Shortened document text for Bonsai; this document will need review.", 0, 0, 91)
	}
	fallback := Classification{ModelContextTruncated: truncated}
	input := fmInput{date.Format("2006-01-02"), filename, text, folders, cfg.RecipientProfiles, examples}
	prompt, err := json.Marshal(input)
	if err != nil {
		return fallback, err
	}
	schema := classificationJSONSchema(folders)
	reporter.Info("llm", "structure", "Extracting structured fields with Bonsai.", 0, 0, 92)
	output, err := bonsai.Complete(ctx, cfg, bonsaiInstructions, string(prompt), schema)
	if err != nil {
		return fallback, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(output), &fields); err != nil {
		return fallback, fmt.Errorf("Bonsai returned invalid classification JSON: %w", err)
	}
	for _, field := range schema["required"].([]string) {
		if value, ok := fields[field]; !ok || string(value) == "null" {
			return fallback, fmt.Errorf("Bonsai classification is missing %s", field)
		}
	}
	var result Classification
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return fallback, fmt.Errorf("Bonsai returned invalid classification JSON: %w", err)
	}
	if !slices.Contains(documentTypeValues, result.DocumentType) || result.Confidence < 0 || result.Confidence > 1 ||
		!slices.Contains(RecipientScopes, result.RecipientScope) ||
		!slices.Contains([]string{"person", "household", "company", "unknown"}, result.RecipientType) ||
		(result.SuggestedFolder != "" && !folderAllowed(result.SuggestedFolder, folders)) {
		return fallback, errors.New("Bonsai returned invalid classification fields")
	}
	result.Source = "bonsai"
	result.ModelContextTruncated = truncated
	if truncated {
		result.Reasons = append(result.Reasons, "Bonsai document text was shortened; review the complete document")
	}
	return result, nil
}
