package classify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"

	"paperless/internal/config"
	"paperless/internal/progress"
)

type Classification struct {
	DocumentType           string          `json:"document_type"`
	Sender                 string          `json:"sender"`
	Recipient              string          `json:"recipient"`
	RecipientType          string          `json:"recipient_type"`
	DocumentDate           string          `json:"document_date"`
	Summary                string          `json:"summary"`
	SuggestedFolder        string          `json:"suggested_folder"`
	SuggestedFilename      string          `json:"suggested_filename"`
	PhysicalOriginalAction string          `json:"physical_original_action"`
	Confidence             float64         `json:"confidence"`
	Reasons                []string        `json:"reasons"`
	Sensitive              bool            `json:"sensitive"`
	Source                 string          `json:"source"`
	FolderRankings         []FolderRanking `json:"folder_rankings"`
}

type FolderRanking struct {
	Folder     string  `json:"folder"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

type ollamaChatRequest struct {
	Model     string          `json:"model"`
	Messages  []ollamaMessage `json:"messages"`
	Stream    bool            `json:"stream"`
	Format    any             `json:"format"`
	Think     bool            `json:"think"`
	Options   map[string]any  `json:"options,omitempty"`
	KeepAlive string          `json:"keep_alive,omitempty"`
}

type ollamaMessage struct {
	Role     string `json:"role"`
	Content  string `json:"content"`
	Thinking string `json:"thinking,omitempty"`
}

type ollamaChatResponse struct {
	Message ollamaMessage `json:"message"`
}

type ollamaChatStreamResponse struct {
	Message ollamaMessage `json:"message"`
	Done    bool          `json:"done"`
	Error   string        `json:"error,omitempty"`
}

type ollamaTagsResponse struct {
	Models []ollamaModel `json:"models"`
}

type ollamaModel struct {
	Name         string   `json:"name"`
	Model        string   `json:"model"`
	Capabilities []string `json:"capabilities"`
}

var (
	dateISO         = regexp.MustCompile(`\b(20\d{2})[-/.](0?[1-9]|1[0-2])[-/.](0?[1-9]|[12]\d|3[01])\b`)
	dateGerman      = regexp.MustCompile(`\b(0?[1-9]|[12]\d|3[01])[.\/-](0?[1-9]|1[0-2])[.\/-](20\d{2})\b`)
	dateGermanShort = regexp.MustCompile(`\b(0?[1-9]|[12]\d|3[01])[.\/-](0?[1-9]|1[0-2])[.\/-](\d{2})\b`)
)

var documentTypeValues = []string{
	"receipt",
	"routine-invoice",
	"insurance-letter",
	"insurance-policy",
	"tax-letter",
	"government-letter",
	"bank-document",
	"medical-document",
	"contract",
	"legal-letter",
	"delivery-receipt",
	"marketing",
	"letter",
	"unknown",
}

func Classify(ctx context.Context, cfg config.Config, text, sourceFilename string, scanDate time.Time, folders []string) Classification {
	return ClassifyWithProgress(ctx, cfg, text, sourceFilename, scanDate, folders, nil)
}

func ClassifyWithProgress(ctx context.Context, cfg config.Config, text, sourceFilename string, scanDate time.Time, folders []string, reporter progress.Reporter) Classification {
	reporter.Info("classify", "rules", "Running local classification rules.", 0, 0, 88)
	base := deterministic(cfg, text, sourceFilename, scanDate, folders)
	if !cfg.LLM.Enabled || cfg.LLM.Provider != "ollama" {
		reporter.Info("classify", "complete", "Using local rules because Ollama is disabled.", 0, 0, 94)
		return base
	}
	llm, err := classifyWithOllama(ctx, cfg, text, sourceFilename, scanDate, folders, base, reporter)
	if err != nil {
		reporter.Warn("llm", "fallback", "Ollama unavailable or invalid; using local rules: "+err.Error(), 0, 0, 94)
		base.Reasons = append(base.Reasons, "ollama unavailable or invalid: "+err.Error())
		return base
	}
	reporter.Info("classify", "merge", "Merging Ollama suggestion with local policy rules.", 0, 0, 95)
	return merge(base, llm, cfg, text, scanDate, folders)
}

func deterministic(cfg config.Config, text, sourceFilename string, scanDate time.Time, folders []string) Classification {
	reasons := []string{}
	docDate, dateReason := extractDate(text, scanDate)
	reasons = append(reasons, dateReason)
	sender, mappedFolder := inferSender(cfg, text, sourceFilename)
	if folderAllowed(mappedFolder, folders) {
		reasons = append(reasons, "known sender mapping")
	} else {
		mappedFolder = ""
	}
	recipient, recipientType := inferRecipient(text)
	if recipient != "" {
		reasons = append(reasons, "recipient found")
	}
	docType, sensitive, typeReasons := inferDocumentType(text)
	reasons = append(reasons, typeReasons...)
	folder := mappedFolder
	if !folderAllowed(folder, folders) {
		folder = ""
	}
	if folder == "" && len(folders) == 1 {
		folder = folders[0]
	}
	if folder == "" && docType == "receipt" {
		folder = receiptFolder(folders)
		if folder != "" {
			reasons = append(reasons, "receipt archive folder")
		}
	}
	action := physicalAction(cfg, docType, sensitive)
	subject := subjectFor(docType, sender)
	filename := BuildFilename(docDate, sender, docType, subject)
	confidence := 0.35
	if dateReason == "document date found" {
		confidence += 0.15
	}
	if mappedFolder != "" {
		confidence += 0.22
	}
	if sender != "unknown" {
		confidence += 0.08
	}
	if docType != "letter" {
		confidence += 0.18
	}
	if folder != "" {
		confidence += 0.08
	}
	if action == "review" {
		confidence -= 0.08
	}
	confidence = clamp(confidence, 0, 0.92)
	return Classification{
		DocumentType:           docType,
		Sender:                 sender,
		Recipient:              recipient,
		RecipientType:          recipientType,
		DocumentDate:           docDate,
		Summary:                strings.TrimSpace(sender + " " + docType),
		SuggestedFolder:        folder,
		SuggestedFilename:      filename,
		PhysicalOriginalAction: action,
		Confidence:             confidence,
		Reasons:                reasons,
		Sensitive:              sensitive,
		Source:                 "rules",
		FolderRankings:         rankSingle(folder, confidence, "rules suggestion"),
	}
}

func classifyWithOllama(ctx context.Context, cfg config.Config, text, sourceFilename string, scanDate time.Time, folders []string, base Classification, reporter progress.Reporter) (Classification, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(cfg.LLM.TimeoutSeconds)*time.Second)
	defer cancel()
	reporter.Info("llm", "model", "Resolving local Qwen 3.5 9B model.", 0, 0, 89)
	model, err := resolveOllamaModel(ctx, cfg)
	if err != nil {
		return Classification{}, err
	}
	reporter.Info("llm", "model", "Using Ollama model "+model+".", 0, 0, 90)
	snippet := text
	if len(snippet) > 12_000 {
		snippet = snippet[:12_000]
	}
	schema := classificationJSONSchema(folders)
	analysisPrompt := fmt.Sprintf(`Analyze this private document OCR for local filing. Focus on the grounded sender, addressee/recipient, document date, document type, sensitivity, useful filename subject, and best matching allowed folder. A retail receipt remains a receipt when it contains VAT, tax numbers, or tax breakdowns; those fields do not make it a tax letter. Do not invent facts. Keep the reasoning concise.

Allowed folders:
%s

Scan date: %s
Source filename: %s
Rule baseline: %s

OCR text:
%s`, strings.Join(folders, "\n"), scanDate.Format("2006-01-02"), sourceFilename, mustJSON(base), snippet)

	reporter.Info("llm", "reasoning", "Running bounded Qwen reasoning pass.", 0, 0, 91)
	analysis, err := sendOllamaChat(ctx, cfg, ollamaChatRequest{
		Model: model,
		Messages: []ollamaMessage{
			{Role: "user", Content: analysisPrompt},
		},
		Stream: reporter != nil,
		Think:  true,
		Options: map[string]any{
			"temperature": 0,
			"num_ctx":     cfg.LLM.ContextTokens,
			"num_predict": cfg.LLM.ReasoningTokens,
		},
		KeepAlive: "5m",
	}, reporter)
	if err != nil {
		return Classification{}, err
	}
	reasoning := strings.TrimSpace(analysis.Message.Thinking + "\n" + analysis.Message.Content)
	if reasoning == "" {
		return Classification{}, errors.New("ollama reasoning pass returned no analysis")
	}
	if len(reasoning) > 8_000 {
		reasoning = reasoning[len(reasoning)-8_000:]
	}
	structurePrompt := fmt.Sprintf(`Fill the enforced document-classification schema from the OCR and prior analysis. Use field descriptions and enums exactly. Use only an allowed folder, or an empty folder when none fits. Do not add facts that are not grounded.

Prior analysis:
%s

OCR text:
%s`, reasoning, snippet)
	reporter.Info("llm", "structure", "Converting Qwen analysis into structured fields.", 0, 0, 93)
	out, err := sendOllamaChat(ctx, cfg, ollamaChatRequest{
		Model: model,
		Messages: []ollamaMessage{
			{Role: "user", Content: structurePrompt},
		},
		Stream: reporter != nil,
		Format: schema,
		Think:  false,
		Options: map[string]any{
			"temperature": 0,
			"num_ctx":     cfg.LLM.ContextTokens,
			"num_predict": cfg.LLM.MaxOutputTokens,
		},
		KeepAlive: cfg.LLM.KeepAlive,
	}, reporter)
	if err != nil {
		return Classification{}, err
	}
	reporter.Info("llm", "parse", "Parsing Ollama JSON response.", 0, 0, 94)
	var c Classification
	payload := ollamaJSONPayload(out)
	if payload == "" {
		return Classification{}, fmt.Errorf("ollama returned no JSON payload")
	}
	if err := json.Unmarshal([]byte(payload), &c); err != nil {
		return Classification{}, err
	}
	c.Source = "ollama"
	return c, nil
}

func sendOllamaChat(ctx context.Context, cfg config.Config, request ollamaChatRequest, reporter progress.Reporter) (ollamaChatResponse, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return ollamaChatResponse{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.LLM.Endpoint, "/")+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return ollamaChatResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ollamaChatResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ollamaChatResponse{}, fmt.Errorf("ollama status %d", resp.StatusCode)
	}
	if reporter != nil {
		return decodeOllamaStream(resp.Body, reporter)
	}
	var out ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return ollamaChatResponse{}, err
	}
	return out, nil
}

func decodeOllamaStream(body io.Reader, reporter progress.Reporter) (ollamaChatResponse, error) {
	decoder := json.NewDecoder(body)
	var content strings.Builder
	var thinking strings.Builder
	loggedThinking := 0
	loggedContent := 0
	for {
		var chunk ollamaChatStreamResponse
		if err := decoder.Decode(&chunk); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return ollamaChatResponse{}, err
		}
		if chunk.Error != "" {
			return ollamaChatResponse{}, errors.New(chunk.Error)
		}
		if chunk.Message.Thinking != "" {
			thinking.WriteString(chunk.Message.Thinking)
			if thinking.Len() >= loggedThinking+2048 || loggedThinking == 0 {
				loggedThinking = thinking.Len()
				reporter.Info("llm", "thinking", fmt.Sprintf("Ollama thinking stream active (%d bytes received).", loggedThinking), 0, 0, 92)
			}
		}
		if chunk.Message.Content != "" {
			content.WriteString(chunk.Message.Content)
			if content.Len() >= loggedContent+1024 || loggedContent == 0 {
				loggedContent = content.Len()
				reporter.Info("llm", "response", fmt.Sprintf("Ollama structured response streaming (%d bytes received).", loggedContent), 0, 0, 93)
			}
		}
		if chunk.Done {
			break
		}
	}
	return ollamaChatResponse{
		Message: ollamaMessage{
			Role:     "assistant",
			Content:  content.String(),
			Thinking: thinking.String(),
		},
	}, nil
}

func classificationJSONSchema(folders []string) map[string]any {
	allowedFolders := schemaFolderValues(folders, true)
	rankingFolder := stringSchema("Archive folder path. Must be one of the allowed folders.")
	if len(folders) > 0 {
		rankingFolder["enum"] = schemaFolderValues(folders, false)
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required": []string{
			"document_type",
			"sender",
			"recipient",
			"recipient_type",
			"document_date",
			"summary",
			"suggested_folder",
			"suggested_filename",
			"physical_original_action",
			"confidence",
			"reasons",
			"sensitive",
			"folder_rankings",
		},
		"properties": map[string]any{
			"document_type": map[string]any{
				"type":        "string",
				"enum":        documentTypeValues,
				"description": "Document category inferred from OCR text. Use unknown when the text is too weak.",
			},
			"sender": map[string]any{
				"type":        "string",
				"description": "Short sender, merchant, authority, or company name grounded in the OCR text. Empty string if not grounded.",
			},
			"recipient": map[string]any{
				"type":        "string",
				"description": "Short addressee or receiver name grounded in the OCR text. Empty string if no receiver is visible.",
			},
			"recipient_type": map[string]any{
				"type":        "string",
				"enum":        []string{"person", "household", "company", "unknown"},
				"description": "Receiver class. Use household for couples, families, shared addressees, or names joined by '&' or 'und'.",
			},
			"document_date": map[string]any{
				"type":        "string",
				"description": "Document date in YYYY-MM-DD. Use empty string if no document date is grounded in OCR.",
			},
			"summary": map[string]any{
				"type":        "string",
				"description": "Short factual summary of what the document is about.",
			},
			"suggested_folder": map[string]any{
				"type":        "string",
				"enum":        allowedFolders,
				"description": "Best archive folder. Use empty string when none of the allowed folders fit.",
			},
			"suggested_filename": map[string]any{
				"type":        "string",
				"description": "Lowercase filename in the form YYYY-MM-DD__sender__doctype__subject.pdf. Use ASCII-ish text and no spaces.",
			},
			"physical_original_action": map[string]any{
				"type":        "string",
				"enum":        []string{"review", "keep_original", "discard_candidate"},
				"description": "Recommendation for the paper original. Sensitive/legal/tax/bank/medical documents should usually be keep_original.",
			},
			"confidence": map[string]any{
				"type":        "number",
				"minimum":     0,
				"maximum":     1,
				"description": "Confidence in the classification and routing suggestion from 0.0 to 1.0.",
			},
			"reasons": map[string]any{
				"type":        "array",
				"maxItems":    8,
				"description": "Short grounded reasons for the classification and routing.",
				"items":       stringSchema("One short grounded reason."),
			},
			"sensitive": map[string]any{
				"type":        "boolean",
				"description": "True for legal, tax, medical, bank, identity, contract, or insurance-policy documents.",
			},
			"folder_rankings": map[string]any{
				"type":        "array",
				"maxItems":    5,
				"description": "Ranked allowed folders that could fit the document.",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"folder", "confidence", "reason"},
					"properties": map[string]any{
						"folder": rankingFolder,
						"confidence": map[string]any{
							"type":        "number",
							"minimum":     0,
							"maximum":     1,
							"description": "Confidence for this folder from 0.0 to 1.0.",
						},
						"reason": stringSchema("Short reason why this folder fits."),
					},
				},
			},
		},
	}
}

func stringSchema(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func schemaFolderValues(folders []string, includeEmpty bool) []string {
	seen := map[string]bool{}
	values := []string{}
	if includeEmpty {
		values = append(values, "")
		seen[""] = true
	}
	for _, folder := range folders {
		folder = strings.Trim(folder, "/")
		if folder == "" || seen[folder] {
			continue
		}
		values = append(values, folder)
		seen[folder] = true
	}
	return values
}

func resolveOllamaModel(ctx context.Context, cfg config.Config) (string, error) {
	configured := strings.TrimSpace(cfg.LLM.Model)
	if configured != "" && !isQwen35Selector(configured) {
		return configured, nil
	}
	if configured == "" {
		configured = "qwen3.5"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.LLM.Endpoint, "/")+"/api/tags", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("ollama status %d", resp.StatusCode)
	}
	var tags ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return "", err
	}
	model, ok := selectQwen35Model(tags.Models)
	if !ok {
		return "", fmt.Errorf("no installed Ollama Qwen 3.5 model found")
	}
	return model, nil
}

func selectQwen35Model(models []ollamaModel) (string, bool) {
	for _, candidate := range []func(ollamaModel) bool{
		func(model ollamaModel) bool { return modelIsQwen35(model) && modelCanThink(model) },
		modelIsQwen35,
	} {
		for _, model := range models {
			if candidate(model) {
				return modelName(model), true
			}
		}
	}
	return "", false
}

func isQwen35Selector(value string) bool {
	normalized := normalizeModelName(value)
	return normalized == "" || normalized == "qwen35"
}

func modelName(model ollamaModel) string {
	if strings.TrimSpace(model.Model) != "" {
		return strings.TrimSpace(model.Model)
	}
	return strings.TrimSpace(model.Name)
}

func modelIsQwen35(model ollamaModel) bool {
	name := normalizeModelName(model.Name + " " + model.Model)
	return strings.Contains(name, "qwen35")
}

func normalizeModelName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer(".", "", "-", "", "_", "", ":", "", "/", "", " ", "")
	return replacer.Replace(value)
}

func modelCanThink(model ollamaModel) bool {
	for _, capability := range model.Capabilities {
		if strings.EqualFold(capability, "thinking") {
			return true
		}
	}
	return false
}

func ollamaJSONPayload(out ollamaChatResponse) string {
	for _, value := range []string{out.Message.Content, out.Message.Thinking} {
		if payload := extractJSONPayload(value); payload != "" {
			return payload
		}
	}
	return ""
}

func extractJSONPayload(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if json.Valid([]byte(value)) {
		return value
	}
	if strings.HasPrefix(value, "```") {
		value = strings.TrimPrefix(value, "```json")
		value = strings.TrimPrefix(value, "```")
		value = strings.TrimSuffix(value, "```")
		value = strings.TrimSpace(value)
		if json.Valid([]byte(value)) {
			return value
		}
	}
	start := strings.Index(value, "{")
	end := strings.LastIndex(value, "}")
	if start >= 0 && end > start {
		candidate := strings.TrimSpace(value[start : end+1])
		if json.Valid([]byte(candidate)) {
			return candidate
		}
	}
	return ""
}

func merge(base, llm Classification, cfg config.Config, text string, scanDate time.Time, folders []string) Classification {
	out := base
	strongReceipt := base.DocumentType == "receipt" && ReceiptLikely(text)
	if llm.DocumentType != "" && llm.DocumentType != "unknown" && !(llm.DocumentType == "letter" && base.DocumentType != "letter" && base.DocumentType != "unknown") && !(strongReceipt && llm.DocumentType != "receipt") {
		out.DocumentType = Slug(llm.DocumentType)
	}
	if strings.TrimSpace(llm.Sender) != "" {
		llmSender := Slug(llm.Sender)
		if compactName(llmSender) == compactName(base.Sender) {
			out.Sender = base.Sender
		} else {
			out.Sender = llmSender
		}
	}
	if strings.TrimSpace(llm.Recipient) != "" && !genericRecipient(llm.Recipient) {
		out.Recipient = Slug(llm.Recipient)
	}
	if normalized := normalizeRecipientType(llm.RecipientType); normalized != "" {
		out.RecipientType = normalized
	}
	if validDate(llm.DocumentDate) && dateGrounded(text, llm.DocumentDate) {
		out.DocumentDate = llm.DocumentDate
	} else if llm.DocumentDate != "" && llm.DocumentDate == scanDate.Format("2006-01-02") {
		out.DocumentDate = llm.DocumentDate
	}
	if strings.TrimSpace(llm.Summary) != "" {
		out.Summary = strings.TrimSpace(llm.Summary)
	}
	if folderAllowed(llm.SuggestedFolder, folders) {
		out.SuggestedFolder = strings.Trim(llm.SuggestedFolder, "/")
	}
	if strongReceipt {
		if folder := receiptFolder(folders); folder != "" {
			out.SuggestedFolder = folder
		}
	}
	out.SuggestedFolder = folderForDocumentYear(out.SuggestedFolder, out.DocumentDate, folders)
	if len(llm.FolderRankings) > 0 {
		out.FolderRankings = sanitizeRankingsForYear(llm.FolderRankings, out.DocumentDate, folders)
	}
	out.Sensitive = isSensitive(out.DocumentType)
	out.PhysicalOriginalAction = physicalAction(cfg, out.DocumentType, out.Sensitive)
	out.SuggestedFilename = BuildFilename(out.DocumentDate, out.Sender, out.DocumentType, subjectFromSummary(out.Summary, out.DocumentType))
	out.Confidence = clamp(llm.Confidence, base.Confidence, 0.98)
	out.Reasons = append([]string{"ollama structured suggestion"}, llm.Reasons...)
	out.Source = "ollama"
	return out
}

func genericRecipient(value string) bool {
	value = Slug(value)
	for _, generic := range []string{"steuerzahler", "steuerzahler-in", "steuerpflichtige", "kunde", "kundin", "patient", "patientin", "empfaenger", "adressat"} {
		if value == generic {
			return true
		}
	}
	return false
}

func folderForDocumentYear(folder, documentDate string, folders []string) string {
	if len(documentDate) < 4 || folder == "" {
		return folder
	}
	documentYear := documentDate[:4]
	for folder != "" && folderHasDifferentYear(folder, documentYear) {
		folder = strings.Trim(strings.TrimSuffix(folder, filepath.Base(folder)), "/")
	}
	if folderAllowed(folder, folders) {
		return folder
	}
	return ""
}

func folderHasDifferentYear(folder, documentYear string) bool {
	for _, part := range strings.Split(filepath.ToSlash(folder), "/") {
		if len(part) == 4 && strings.HasPrefix(part, "20") && part != documentYear {
			if _, err := time.Parse("2006", part); err == nil {
				return true
			}
		}
	}
	return false
}

func sanitizeRankingsForYear(rankings []FolderRanking, documentDate string, folders []string) []FolderRanking {
	rankings = sanitizeRankings(rankings, folders)
	if len(documentDate) < 4 {
		return rankings
	}
	documentYear := documentDate[:4]
	out := rankings[:0]
	seen := map[string]bool{}
	for _, ranking := range rankings {
		ranking.Folder = folderForDocumentYear(ranking.Folder, documentDate, folders)
		if ranking.Folder == "" || seen[ranking.Folder] || folderHasDifferentYear(ranking.Folder, documentYear) {
			continue
		}
		seen[ranking.Folder] = true
		out = append(out, ranking)
	}
	return out
}

func extractDate(text string, scanDate time.Time) (string, string) {
	if match := dateISO.FindStringSubmatch(text); match != nil {
		if parsed, ok := parseDate(match[1], match[2], match[3]); ok {
			return parsed, "document date found"
		}
	}
	if match := dateGerman.FindStringSubmatch(text); match != nil {
		if parsed, ok := parseDate(match[3], match[2], match[1]); ok {
			return parsed, "document date found"
		}
	}
	if match := dateGermanShort.FindStringSubmatch(text); match != nil {
		if parsed, ok := parseDate(expandShortYear(match[3]), match[2], match[1]); ok {
			return parsed, "document date found"
		}
	}
	return scanDate.Format("2006-01-02"), "scan date used"
}

func expandShortYear(year string) string {
	if len(year) != 2 {
		return year
	}
	return "20" + year
}

func parseDate(year, month, day string) (string, bool) {
	parsed, err := time.Parse("2006-1-2", fmt.Sprintf("%s-%s-%s", year, month, day))
	if err != nil {
		return "", false
	}
	return parsed.Format("2006-01-02"), true
}

func inferSender(cfg config.Config, text, sourceFilename string) (string, string) {
	haystack := strings.ToLower(sourceFilename + "\n" + text)
	keys := make([]string, 0, len(cfg.SenderFolders))
	for key := range cfg.SenderFolders {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, sender := range keys {
		if strings.Contains(Slug(haystack), Slug(sender)) {
			return Slug(sender), strings.Trim(cfg.SenderFolders[sender], "/")
		}
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if len(line) >= 3 && len(line) <= 60 && hasLetter(line) {
			return Slug(repairOCRWordSplits(line)), ""
		}
	}
	return "unknown", ""
}

func repairOCRWordSplits(line string) string {
	fields := strings.Fields(line)
	var repaired []string
	for index := 0; index < len(fields); index++ {
		current := fields[index]
		if index+1 < len(fields) && len([]rune(current)) <= 2 && startsLowercase(fields[index+1]) {
			repaired = append(repaired, current+fields[index+1])
			index++
			continue
		}
		repaired = append(repaired, current)
	}
	return strings.Join(repaired, " ")
}

func startsLowercase(value string) bool {
	for _, r := range value {
		return unicode.IsLower(r)
	}
	return false
}

func compactName(value string) string {
	return strings.ReplaceAll(Slug(value), "-", "")
}

func inferRecipient(text string) (string, string) {
	lines := cleanLines(text)
	for index, line := range lines {
		if index > 80 {
			break
		}
		marker, rest, ok := recipientMarker(line)
		if !ok {
			continue
		}
		parts := []string{}
		if rest != "" && looksLikeRecipientName(rest) {
			parts = append(parts, rest)
		}
		for next := index + 1; next < len(lines) && next <= index+4; next++ {
			candidate := lines[next]
			if !looksLikeRecipientName(candidate) {
				break
			}
			parts = append(parts, candidate)
		}
		if len(parts) == 0 {
			continue
		}
		recipient := strings.Join(parts, " ")
		return Slug(recipient), recipientType(marker, recipient)
	}
	return "", "unknown"
}

func cleanLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.Join(strings.Fields(strings.TrimSpace(line)), " ")
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func recipientMarker(line string) (string, string, bool) {
	lower := strings.ToLower(strings.TrimSpace(line))
	markers := []string{"herrn", "herr", "frau", "familie", "eheleute", "firma"}
	for _, marker := range markers {
		if lower == marker {
			return marker, "", true
		}
		if strings.HasPrefix(lower, marker+" ") {
			return marker, strings.TrimSpace(line[len(marker):]), true
		}
	}
	return "", "", false
}

func looksLikeRecipientName(line string) bool {
	if line == "" || !hasLetter(line) {
		return false
	}
	lower := strings.ToLower(line)
	if strings.HasPrefix(lower, "sehr geehrt") || strings.Contains(lower, "finanzamt") {
		return false
	}
	for _, r := range line {
		if unicode.IsDigit(r) {
			return false
		}
	}
	return len([]rune(line)) <= 80
}

func recipientType(marker, recipient string) string {
	lower := strings.ToLower(marker + " " + recipient)
	if containsAny(lower, "firma", " gmbh", " ug ", " ag ", " kg ", " ohg", " ev", " e.v.") {
		return "company"
	}
	if containsAny(lower, "familie", "eheleute", " & ", " und ") {
		return "household"
	}
	if containsAny(lower, "herr", "frau") {
		return "person"
	}
	return "unknown"
}

func normalizeRecipientType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "person", "individual":
		return "person"
	case "household", "couple", "family":
		return "household"
	case "company", "business", "organization", "organisation":
		return "company"
	case "unknown":
		return "unknown"
	default:
		return ""
	}
}

func inferDocumentType(text string) (string, bool, []string) {
	lower := strings.ToLower(text)
	if ReceiptLikely(lower) {
		return "receipt", false, []string{"retail receipt markers found"}
	}
	sensitive := map[string]string{
		"versicherungsschein": "insurance-policy",
		"police":              "insurance-policy",
		"finanzamt":           "tax-letter",
		"steuer":              "tax-letter",
		"gericht":             "legal-letter",
		"rechtsanwalt":        "legal-letter",
		"vertrag":             "contract",
		"contract":            "contract",
		"diagnose":            "medical-document",
		"arzt":                "medical-document",
		"konto":               "bank-document",
		"personalausweis":     "identity-document",
		"passport":            "identity-document",
	}
	keys := make([]string, 0, len(sensitive))
	for key := range sensitive {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if strings.Contains(lower, key) {
			return sensitive[key], true, []string{"sensitive hint: " + key}
		}
	}
	if containsAny(lower, "quittung", "kassenbon", "receipt", "mwst", "vat", "total", "summe", "gesamt", "eur") {
		return "receipt", false, []string{"receipt-like total/tax terms"}
	}
	if containsAny(lower, "rechnung", "invoice", "rechnungsnummer") {
		return "routine-invoice", false, []string{"invoice terms found"}
	}
	if containsAny(lower, "lieferung", "delivery", "shipment", "paket") {
		return "delivery-receipt", false, []string{"delivery terms found"}
	}
	if containsAny(lower, "newsletter", "werbung", "marketing") {
		return "marketing", false, []string{"marketing terms found"}
	}
	return "letter", false, []string{"no strong document type signal"}
}

func ReceiptLikely(text string) bool {
	lower := strings.ToLower(text)
	if containsAny(lower, "kassenbon", "kundebeleg", "beleg-nr", "beleg nr", "receipt") {
		return true
	}
	markers := 0
	for _, marker := range []string{"gesamtbetrag", "terminal-id", "terminal id", "kartennummer", "zahlung", "mwst", "brutto", "netto", "eur", "visa", "mastercard"} {
		if strings.Contains(lower, marker) {
			markers++
		}
	}
	return markers >= 3
}

func receiptFolder(folders []string) string {
	preferences := []string{"belege", "receipts-general", "receipts", "quittungen"}
	for _, preference := range preferences {
		for _, folder := range folders {
			if Slug(filepath.Base(folder)) == preference {
				return strings.Trim(folder, "/")
			}
		}
	}
	return ""
}

func physicalAction(cfg config.Config, docType string, sensitive bool) string {
	if sensitive || slices.Contains(cfg.Policy.KeepDocumentTypes, docType) {
		return "keep_original"
	}
	if slices.Contains(cfg.Policy.DiscardDocumentTypes, docType) {
		return "discard_candidate"
	}
	return "review"
}

func isSensitive(docType string) bool {
	return strings.Contains(docType, "contract") ||
		strings.Contains(docType, "tax") ||
		strings.Contains(docType, "legal") ||
		strings.Contains(docType, "medical") ||
		strings.Contains(docType, "bank") ||
		strings.Contains(docType, "identity") ||
		strings.Contains(docType, "policy")
}

func BuildFilename(date, sender, docType, subject string) string {
	if !validDate(date) {
		date = time.Now().Format("2006-01-02")
	}
	if Slug(docType) == "receipt" {
		return date + "__" + Slug(sender) + "__receipt.pdf"
	}
	return date + "__" + Slug(sender) + "__" + Slug(docType) + "__" + Slug(subject) + ".pdf"
}

func NormalizeDocumentType(value string) string {
	value = Slug(value)
	if slices.Contains(documentTypeValues, value) {
		return value
	}
	return ""
}

func DocumentTypeSensitive(value string) bool {
	return isSensitive(NormalizeDocumentType(value))
}

func Slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var out strings.Builder
	lastDash := false
	for _, r := range value {
		if replacement, ok := asciiReplacement(r); ok {
			out.WriteString(replacement)
			lastDash = false
			continue
		}
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			out.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			out.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(out.String(), "-")
}

func asciiReplacement(r rune) (string, bool) {
	switch r {
	case 'ä':
		return "ae", true
	case 'ö':
		return "oe", true
	case 'ü':
		return "ue", true
	case 'ß':
		return "ss", true
	case 'à', 'á', 'â', 'ã', 'å', 'ā', 'ă', 'ą':
		return "a", true
	case 'ç', 'ć', 'č':
		return "c", true
	case 'ď', 'đ':
		return "d", true
	case 'è', 'é', 'ê', 'ë', 'ē', 'ė', 'ę':
		return "e", true
	case 'ì', 'í', 'î', 'ï', 'ī', 'į':
		return "i", true
	case 'ñ', 'ń':
		return "n", true
	case 'ò', 'ó', 'ô', 'õ', 'ø', 'ō':
		return "o", true
	case 'ř':
		return "r", true
	case 'ś', 'š':
		return "s", true
	case 'ť':
		return "t", true
	case 'ù', 'ú', 'û', 'ū':
		return "u", true
	case 'ý', 'ÿ':
		return "y", true
	case 'ź', 'ż', 'ž':
		return "z", true
	default:
		return "", false
	}
}

func subjectFor(docType, sender string) string {
	if docType == "receipt" && sender != "" && sender != "unknown" {
		return sender
	}
	if docType == "routine-invoice" {
		return "invoice"
	}
	return docType
}

func subjectFromSummary(summary, docType string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return docType
	}
	slug := Slug(summary)
	parts := strings.Split(slug, "-")
	if len(parts) > 6 {
		parts = parts[:6]
	}
	return strings.Join(parts, "-")
}

func validDate(value string) bool {
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

func dateGrounded(text, isoDate string) bool {
	if strings.Contains(text, isoDate) {
		return true
	}
	parsed, err := time.Parse("2006-01-02", isoDate)
	if err != nil {
		return false
	}
	return strings.Contains(text, parsed.Format("02.01.2006")) ||
		strings.Contains(text, parsed.Format("2.1.2006")) ||
		strings.Contains(text, parsed.Format("02.01.06")) ||
		strings.Contains(text, parsed.Format("2.1.06")) ||
		strings.Contains(text, parsed.Format("02/01/06")) ||
		strings.Contains(text, parsed.Format("02/01/2006"))
}

func folderAllowed(folder string, folders []string) bool {
	folder = strings.Trim(folder, "/")
	return folder != "" && slices.Contains(folders, folder)
}

func sanitizeRankings(rankings []FolderRanking, folders []string) []FolderRanking {
	var out []FolderRanking
	for _, ranking := range rankings {
		ranking.Folder = strings.Trim(ranking.Folder, "/")
		if folderAllowed(ranking.Folder, folders) {
			ranking.Confidence = clamp(ranking.Confidence, 0, 1)
			out = append(out, ranking)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Confidence > out[j].Confidence })
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

func rankSingle(folder string, confidence float64, reason string) []FolderRanking {
	if folder == "" {
		return nil
	}
	return []FolderRanking{{Folder: folder, Confidence: confidence, Reason: reason}}
}

func containsAny(text string, values ...string) bool {
	for _, value := range values {
		if strings.Contains(text, value) {
			return true
		}
	}
	return false
}

func hasLetter(value string) bool {
	for _, r := range value {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func clamp(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func SafePDFName(name string) string {
	return Slug(strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))) + ".pdf"
}
