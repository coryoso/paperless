package classify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"paperless/internal/config"
	"paperless/internal/progress"
)

func TestDeterministicReceiptClassification(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Enabled = false
	cfg.Policy.KnownFolders = []string{"09 Rechnungen und Belege/Belege"}
	result := Classify(
		t.Context(),
		cfg,
		"REWE Markt\n26.07.2026\nMwSt\nTotal EUR 12,34",
		"scan.pdf",
		time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC),
		cfg.Policy.KnownFolders,
	)
	if result.DocumentType != "receipt" {
		t.Fatalf("document type = %q", result.DocumentType)
	}
	if result.SuggestedFolder != "09 Rechnungen und Belege/Belege" {
		t.Fatalf("folder = %q", result.SuggestedFolder)
	}
	if result.PhysicalOriginalAction != "discard_candidate" {
		t.Fatalf("paper action = %q", result.PhysicalOriginalAction)
	}
	if result.DocumentDate != "2026-07-26" {
		t.Fatalf("date = %q", result.DocumentDate)
	}
}

func TestReceiptTaxBreakdownDoesNotBecomeTaxLetter(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Enabled = false
	cfg.Policy.KnownFolders = []string{"09 Rechnungen und Belege/Belege"}
	result := Classify(
		t.Context(),
		cfg,
		"Total Tankstelle\nBeleg-Nr. 302/002\nGesamtbetrag 15,17 EUR\nNetto 12,75 MwSt 2,42 Brutto 15,17\nSteuernummer DE 296/513/971\nVISA KUNDENBELEG",
		"scan.pdf",
		time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC),
		cfg.Policy.KnownFolders,
	)
	if result.DocumentType != "receipt" || result.Sensitive {
		t.Fatalf("classification = %#v", result)
	}
	if result.SuggestedFolder != "09 Rechnungen und Belege/Belege" {
		t.Fatalf("folder = %q", result.SuggestedFolder)
	}
	if result.SuggestedFilename != "2026-08-29__total-tankstelle__receipt.pdf" {
		t.Fatalf("filename = %q", result.SuggestedFilename)
	}
}

func TestMergeDoesNotReplaceStrongReceiptWithTaxLetter(t *testing.T) {
	cfg := config.Default()
	cfg.Policy.KnownFolders = []string{"09 Rechnungen und Belege/Belege"}
	text := "KUNDENBELEG Gesamtbetrag 15,17 EUR MwSt VISA Steuernummer"
	base := deterministic(cfg, text, "scan.pdf", time.Now(), cfg.Policy.KnownFolders)
	llm := Classification{DocumentType: "tax-letter", Confidence: .96, SuggestedFolder: "Admin/Tax"}
	got := merge(base, llm, cfg, text, time.Now(), cfg.Policy.KnownFolders)
	if got.DocumentType != "receipt" || got.Sensitive {
		t.Fatalf("classification = %#v", got)
	}
	if got.SuggestedFolder != "09 Rechnungen und Belege/Belege" {
		t.Fatalf("folder = %q", got.SuggestedFolder)
	}
}

func TestSensitiveClassificationKeepsOriginal(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Enabled = false
	result := Classify(
		t.Context(),
		cfg,
		"Allianz Versicherungsschein\nPolice\n01.04.2026",
		"allianz.pdf",
		time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC),
		cfg.Policy.KnownFolders,
	)
	if result.PhysicalOriginalAction != "keep_original" {
		t.Fatalf("paper action = %q", result.PhysicalOriginalAction)
	}
	if !result.Sensitive {
		t.Fatal("expected sensitive document")
	}
}

func TestGermanShortDateIsUsedForTaxLetter(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Enabled = false
	result := Classify(
		t.Context(),
		cfg,
		"Finanzamt Musterstadt\nTM 25.02.26\nHerrn\nAlex\nExample\nBeispielstrasse 32\nSehr geehrter Steuerzahler,\ndas Finanzamt hat Ihnen die Steuernummer zugeteilt.",
		"newnewscan2.pdf",
		time.Date(2026, 4, 4, 22, 26, 0, 0, time.UTC),
		cfg.Policy.KnownFolders,
	)
	if result.DocumentDate != "2026-02-25" {
		t.Fatalf("date = %q", result.DocumentDate)
	}
	if result.DocumentType != "tax-letter" {
		t.Fatalf("document type = %q", result.DocumentType)
	}
	if result.SuggestedFolder != "" {
		t.Fatalf("folder = %q", result.SuggestedFolder)
	}
	if result.Recipient != "alex-example" {
		t.Fatalf("recipient = %q", result.Recipient)
	}
	if result.RecipientType != "person" {
		t.Fatalf("recipient type = %q", result.RecipientType)
	}
	wantFilename := "2026-02-25__finanzamt-musterstadt__tax-letter__tax-letter.pdf"
	if result.SuggestedFilename != wantFilename {
		t.Fatalf("filename = %q, want %q", result.SuggestedFilename, wantFilename)
	}
	if result.PhysicalOriginalAction != "keep_original" {
		t.Fatalf("paper action = %q", result.PhysicalOriginalAction)
	}
}

func TestExtractDateRejectsInvalidShortDate(t *testing.T) {
	scanDate := time.Date(2026, 4, 4, 22, 26, 0, 0, time.UTC)
	date, reason := extractDate("Finanzamt 31.02.26", scanDate)
	if date != "2026-04-04" {
		t.Fatalf("date = %q", date)
	}
	if reason != "scan date used" {
		t.Fatalf("reason = %q", reason)
	}
}

func TestSlugUsesASCIIGermanTransliteration(t *testing.T) {
	got := Slug("Finanzamt Königs Wusterhausen - Straße")
	want := "finanzamt-koenigs-wusterhausen-strasse"
	if got != want {
		t.Fatalf("slug = %q, want %q", got, want)
	}
}

func TestInferSenderRepairsSplitMerchantHeading(t *testing.T) {
	cfg := config.Default()
	sender, _ := inferSender(cfg, "Total Ta nkstelle\nMichendorf Sued", "scan.pdf")
	if sender != "total-tankstelle" {
		t.Fatalf("sender = %q", sender)
	}
}

func TestMergePrefersCleanBaselineSenderOverEquivalentSplitName(t *testing.T) {
	cfg := config.Default()
	base := Classification{DocumentType: "receipt", Sender: "total-tankstelle", DocumentDate: "2025-06-07", SuggestedFolder: "09 Rechnungen und Belege/Belege"}
	llm := Classification{DocumentType: "receipt", Sender: "total-ta-nkstelle", SuggestedFolder: "Belege", Confidence: .9}
	got := merge(base, llm, cfg, "KUNDENBELEG Gesamtbetrag EUR MwSt VISA 07.06.2025", time.Now(), cfg.Policy.KnownFolders)
	if got.Sender != "total-tankstelle" || got.SuggestedFilename != "2025-06-07__total-tankstelle__receipt.pdf" {
		t.Fatalf("classification = %#v", got)
	}
}

func TestInferRecipientDistinguishesHouseholdAndCompany(t *testing.T) {
	recipient, recipientType := inferRecipient("Familie\nKarl & Maren\nMusterweg 1\n12345 Berlin")
	if recipient != "karl-maren" {
		t.Fatalf("household recipient = %q", recipient)
	}
	if recipientType != "household" {
		t.Fatalf("household type = %q", recipientType)
	}

	recipient, recipientType = inferRecipient("Firma Beispiel GmbH\nHauptstrasse 1")
	if recipient != "beispiel-gmbh" {
		t.Fatalf("company recipient = %q", recipient)
	}
	if recipientType != "company" {
		t.Fatalf("company type = %q", recipientType)
	}
}

func TestClassifyWithOllamaUsesChatEndpointWithThinking(t *testing.T) {
	var requestPath string
	var requests []ollamaChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen3.5:9b-q4_K_M","model":"qwen3.5:9b-q4_K_M","capabilities":["completion","thinking"]}]}`))
			return
		case "/api/chat":
			requestPath = r.URL.Path
			var request ollamaChatRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			requests = append(requests, request)
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"{\"document_type\":\"tax-letter\",\"sender\":\"Finanzamt\",\"recipient\":\"Alex Example\",\"recipient_type\":\"person\",\"document_date\":\"2026-02-25\",\"summary\":\"Finanzamt letter\",\"suggested_folder\":\"Admin/Tax\",\"suggested_filename\":\"2026-02-25__finanzamt__tax-letter__finanzamt-letter.pdf\",\"physical_original_action\":\"keep_original\",\"confidence\":0.93,\"reasons\":[\"tax office sender\"],\"sensitive\":true,\"folder_rankings\":[{\"folder\":\"Admin/Tax\",\"confidence\":0.93,\"reason\":\"tax authority\"}]}"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.LLM.Endpoint = server.URL
	cfg.LLM.Model = "qwen3.5:9b-q4_K_M"
	cfg.Policy.KnownFolders = append(cfg.Policy.KnownFolders, "Admin/Tax")
	base := deterministic(
		cfg,
		"Finanzamt Koenigs Wusterhausen\nTM 25.02.26",
		"newnewscan2.pdf",
		time.Date(2026, 4, 4, 22, 26, 0, 0, time.UTC),
		cfg.Policy.KnownFolders,
	)
	result, err := classifyWithOllama(
		t.Context(),
		cfg,
		"Finanzamt Koenigs Wusterhausen\nTM 25.02.26",
		"newnewscan2.pdf",
		time.Date(2026, 4, 4, 22, 26, 0, 0, time.UTC),
		cfg.Policy.KnownFolders,
		base,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if requestPath != "/api/chat" {
		t.Fatalf("path = %q", requestPath)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %#v", requests)
	}
	reasoningRequest, structuredRequest := requests[0], requests[1]
	if reasoningRequest.Model != "qwen3.5:9b-q4_K_M" || structuredRequest.Model != reasoningRequest.Model {
		t.Fatalf("models = %q, %q", reasoningRequest.Model, structuredRequest.Model)
	}
	if !reasoningRequest.Think || structuredRequest.Think {
		t.Fatalf("thinking flags = %v, %v", reasoningRequest.Think, structuredRequest.Think)
	}
	if reasoningRequest.Options["num_ctx"] != float64(16_384) {
		t.Fatalf("num_ctx = %#v", reasoningRequest.Options["num_ctx"])
	}
	if reasoningRequest.Options["num_predict"] != float64(2_048) || structuredRequest.Options["num_predict"] != float64(2_048) {
		t.Fatalf("num_predict = %#v, %#v", reasoningRequest.Options["num_predict"], structuredRequest.Options["num_predict"])
	}
	if reasoningRequest.KeepAlive != "5m" || structuredRequest.KeepAlive != "0" {
		t.Fatalf("keep_alive = %q, %q", reasoningRequest.KeepAlive, structuredRequest.KeepAlive)
	}
	schema := requireSchema(t, structuredRequest.Format)
	requireSchemaEnum(t, schema, "document_type", "tax-letter")
	requireSchemaEnum(t, schema, "recipient_type", "household")
	requireSchemaEnum(t, schema, "suggested_folder", "Admin/Tax")
	requireRequiredField(t, schema, "recipient")
	requireRequiredField(t, schema, "folder_rankings")
	if reasoningRequest.Stream || structuredRequest.Stream {
		t.Fatal("expected non-streaming request without a progress reporter")
	}
	if len(structuredRequest.Messages) != 1 || structuredRequest.Messages[0].Role != "user" || !strings.Contains(structuredRequest.Messages[0].Content, "OCR text:") {
		t.Fatalf("unexpected messages: %#v", structuredRequest.Messages)
	}
	if !strings.Contains(structuredRequest.Messages[0].Content, "Prior analysis:") {
		t.Fatal("expected structured pass to include the reasoning result")
	}
	if result.Source != "ollama" {
		t.Fatalf("source = %q", result.Source)
	}
	if result.Sender != "Finanzamt" {
		t.Fatalf("sender = %q", result.Sender)
	}
	if result.SuggestedFolder != "Admin/Tax" {
		t.Fatalf("folder = %q", result.SuggestedFolder)
	}
	if result.Recipient != "Alex Example" {
		t.Fatalf("recipient = %q", result.Recipient)
	}
}

func TestOllamaJSONPayloadCanUseThinkingField(t *testing.T) {
	payload := ollamaJSONPayload(ollamaChatResponse{
		Message: ollamaMessage{
			Thinking: `I should classify this as tax.
{"document_type":"tax-letter","sender":"Finanzamt","document_date":"2026-02-25","summary":"Tax office letter","suggested_folder":"Admin/Tax","suggested_filename":"2026-02-25__finanzamt__tax-letter__letter.pdf","confidence":0.91,"reasons":["tax office sender"],"folder_rankings":[]}`,
		},
	})
	if payload == "" {
		t.Fatal("expected JSON payload from thinking field")
	}
	var result Classification
	if err := json.Unmarshal([]byte(payload), &result); err != nil {
		t.Fatal(err)
	}
	if result.DocumentType != "tax-letter" {
		t.Fatalf("document type = %q", result.DocumentType)
	}
}

func TestResolveOllamaModelSelectsInstalledQwen35Flavor(t *testing.T) {
	var chatModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen3.4:latest","model":"qwen3.4:latest","capabilities":["completion","thinking"]},{"name":"qwen3.5:latest","model":"qwen3.5:latest","capabilities":["completion","thinking"]}]}`))
		case "/api/chat":
			var request ollamaChatRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			chatModel = request.Model
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"{\"document_type\":\"tax-letter\",\"sender\":\"Finanzamt\",\"document_date\":\"2026-02-25\",\"summary\":\"Finanzamt letter\",\"suggested_folder\":\"Admin/Tax\",\"suggested_filename\":\"2026-02-25__finanzamt__tax-letter__letter.pdf\",\"confidence\":0.93,\"reasons\":[],\"folder_rankings\":[]}"}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.LLM.Endpoint = server.URL
	cfg.LLM.Model = "qwen3.5"
	base := deterministic(cfg, "Finanzamt 25.02.26", "scan.pdf", time.Date(2026, 2, 25, 0, 0, 0, 0, time.UTC), cfg.Policy.KnownFolders)
	if _, err := classifyWithOllama(t.Context(), cfg, "Finanzamt 25.02.26", "scan.pdf", time.Now(), cfg.Policy.KnownFolders, base, nil); err != nil {
		t.Fatal(err)
	}
	if chatModel != "qwen3.5:latest" {
		t.Fatalf("chat model = %q", chatModel)
	}
}

func TestClassifyWithOllamaStreamsProgress(t *testing.T) {
	var request ollamaChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tags":
			_, _ = w.Write([]byte(`{"models":[{"name":"qwen3.5:latest","model":"qwen3.5:latest","capabilities":["completion","thinking"]}]}`))
		case "/api/chat":
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","thinking":"checking sender and date"}}` + "\n"))
			_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"{\"document_type\":\"tax-letter\",\"sender\":\"Finanzamt\",\"recipient\":\"Alex Example\",\"recipient_type\":\"person\",\"document_date\":\"2026-02-25\",\"summary\":\"Tax letter\",\"suggested_folder\":\"Admin/Tax\",\"suggested_filename\":\"2026-02-25__finanzamt__tax-letter__tax-letter.pdf\",\"confidence\":0.94,\"reasons\":[\"tax office\"],\"folder_rankings\":[{\"folder\":\"Admin/Tax\",\"confidence\":0.94,\"reason\":\"tax\"}]}"}}` + "\n"))
			_, _ = w.Write([]byte(`{"done":true}` + "\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := config.Default()
	cfg.LLM.Endpoint = server.URL
	cfg.LLM.Model = "qwen3.5"
	base := deterministic(cfg, "Finanzamt 25.02.26", "scan.pdf", time.Now(), cfg.Policy.KnownFolders)
	var events []progress.Event
	result, err := classifyWithOllama(
		t.Context(),
		cfg,
		"Finanzamt 25.02.26",
		"scan.pdf",
		time.Now(),
		cfg.Policy.KnownFolders,
		base,
		progress.Reporter(func(event progress.Event) {
			events = append(events, event)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !request.Stream {
		t.Fatal("expected streaming request with a progress reporter")
	}
	if result.Source != "ollama" {
		t.Fatalf("source = %q", result.Source)
	}
	if result.Recipient != "Alex Example" {
		t.Fatalf("recipient = %q", result.Recipient)
	}
	if !progressEventContains(events, "thinking stream active") {
		t.Fatalf("missing thinking progress event: %#v", events)
	}
	if !progressEventContains(events, "structured response streaming") {
		t.Fatalf("missing response progress event: %#v", events)
	}
}

func TestClassificationJSONSchemaConstrainsFoldersAndFields(t *testing.T) {
	schema := classificationJSONSchema([]string{"Admin/Tax", "Receipts/General"})
	requireSchemaEnum(t, schema, "document_type", "receipt")
	requireSchemaEnum(t, schema, "recipient_type", "company")
	requireSchemaEnum(t, schema, "suggested_folder", "")
	requireSchemaEnum(t, schema, "suggested_folder", "Receipts/General")
	requireRequiredField(t, schema, "physical_original_action")

	properties := schema["properties"].(map[string]any)
	rankings := properties["folder_rankings"].(map[string]any)
	item := rankings["items"].(map[string]any)
	itemProperties := item["properties"].(map[string]any)
	folder := itemProperties["folder"].(map[string]any)
	if containsValue(schemaStringValues(t, folder["enum"]), "") {
		t.Fatal("folder ranking enum should not include empty folder")
	}
	if !containsValue(schemaStringValues(t, folder["enum"]), "Admin/Tax") {
		t.Fatalf("folder ranking enum = %#v", folder["enum"])
	}
}

func TestMergeKeepsSpecificTypeAndRejectsGenericRecipient(t *testing.T) {
	cfg := config.Default()
	base := Classification{DocumentType: "tax-letter", Sender: "finanzamt", DocumentDate: "2026-02-25", SuggestedFolder: "Finanzamt", Sensitive: true}
	llm := Classification{DocumentType: "letter", Recipient: "Steuerzahler/in", DocumentDate: "2026-02-25", SuggestedFolder: "Finanzamt", Confidence: .95}
	got := merge(base, llm, cfg, "25.02.26", time.Date(2026, 2, 25, 0, 0, 0, 0, time.UTC), []string{"Finanzamt"})
	if got.DocumentType != "tax-letter" || !got.Sensitive {
		t.Fatalf("classification = %#v", got)
	}
	if got.Recipient != "" {
		t.Fatalf("recipient = %q", got.Recipient)
	}
}

func TestFolderForDocumentYearFallsBackToCompatibleParent(t *testing.T) {
	folders := []string{"Finanzamt", "Finanzamt/Steuererklaerung", "Finanzamt/Steuererklaerung/2024", "Finanzamt/Steuererklaerung/2024/raw"}
	got := folderForDocumentYear("Finanzamt/Steuererklaerung/2024/raw", "2026-02-25", folders)
	if got != "Finanzamt/Steuererklaerung" {
		t.Fatalf("folder = %q", got)
	}
}

func progressEventContains(events []progress.Event, text string) bool {
	for _, event := range events {
		if strings.Contains(event.Message, text) {
			return true
		}
	}
	return false
}

func requireSchema(t *testing.T, value any) map[string]any {
	t.Helper()
	schema, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("format is %T, want schema object", value)
	}
	if schema["type"] != "object" {
		t.Fatalf("schema type = %#v", schema["type"])
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("additionalProperties = %#v", schema["additionalProperties"])
	}
	return schema
}

func requireSchemaEnum(t *testing.T, schema map[string]any, field, want string) {
	t.Helper()
	properties := schema["properties"].(map[string]any)
	property := properties[field].(map[string]any)
	values := schemaStringValues(t, property["enum"])
	if !containsValue(values, want) {
		t.Fatalf("%s enum = %#v, missing %q", field, values, want)
	}
}

func requireRequiredField(t *testing.T, schema map[string]any, want string) {
	t.Helper()
	required := schemaStringValues(t, schema["required"])
	if !containsValue(required, want) {
		t.Fatalf("required = %#v, missing %q", required, want)
	}
}

func schemaStringValues(t *testing.T, value any) []string {
	t.Helper()
	switch values := value.(type) {
	case []string:
		return values
	case []any:
		out := make([]string, 0, len(values))
		for _, value := range values {
			text, ok := value.(string)
			if !ok {
				t.Fatalf("schema string array contains %T", value)
			}
			out = append(out, text)
		}
		return out
	default:
		t.Fatalf("schema string array is %T", value)
		return nil
	}
}

func containsValue(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
