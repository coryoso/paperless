package classify

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"paperless/internal/config"
	"paperless/internal/progress"
)

const bonsaiTestResponse = `{"document_type":"tax-letter","sender":"Finanzamt","recipient":"Alex Example","recipient_type":"person","recipient_scope":"personal","recipient_evidence":"Herrn Alex Example","document_date":"2026-02-25","summary":"Tax notice","suggested_folder":"Tax/2026","suggested_filename":"tax.pdf","physical_original_action":"discard_candidate","confidence":0.95,"reasons":["Personal tax notice"],"sensitive":false,"folder_rankings":[]}`

func TestBonsaiLiveClassification(t *testing.T) {
	endpoint := os.Getenv("PAPERLESS_BONSAI_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("set PAPERLESS_BONSAI_TEST_ENDPOINT to test a running Bonsai-8B server")
	}
	cfg := config.Default()
	cfg.LLM.Provider = "bonsai"
	cfg.Bonsai.Endpoint = endpoint
	text := "Northstar Office Supplies\nInvoice TEST-2026-0919\nInvoice date: 19 September 2026\nCustomer: Alex Example\n10 notebooks: EUR 40.00\n5 pens: EUR 10.00\nVAT (19%): EUR 9.50\nTotal due: EUR 59.50\nPayment due: 3 October 2026"
	result := Classify(t.Context(), cfg, text, "invoice.pdf", time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC), []string{"Invoices"})
	if result.Source != "bonsai" || result.DocumentType != "routine-invoice" || !strings.Contains(result.Sender, "northstar") || result.DocumentDate != "2026-09-19" || result.SuggestedFolder != "Invoices" {
		t.Fatalf("live classification = %+v", result)
	}
}

func TestBonsaiLiveRecipientContext(t *testing.T) {
	endpoint := os.Getenv("PAPERLESS_BONSAI_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("set PAPERLESS_BONSAI_TEST_ENDPOINT to test a running Bonsai-8B server")
	}
	for _, tt := range []struct{ name, recipient, body, scope, folder string }{
		{"joint business without suffix", "Herren Alex Example und Robin Sample", "Mahnung\nGewerbesteuer für den gemeinsam betriebenen Gewerbebetrieb.\nDer Betrag von EUR 100 ist seit dem 01.09.2026 überfällig.", "gbr", "GbR/Steuern"},
		{"private couple", "Eheleute Alex Example und Robin Sample", "Einkommensteuerbescheid 2026\nIhre gemeinsame private Einkommensteuer beträgt EUR 100.", "personal", "Privat/Steuern"},
		{"person receiving company invoice", "Herrn Alex Example", "Mahnung\nIhre private Bestellung eines Sofas, Rechnung 123, bleibt unbezahlt.\nOffener Betrag EUR 100. Bitte bezahlen Sie bis 30.09.2026.", "personal", "Privat/Rechnungen"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.LLM.Provider = "bonsai"
			cfg.Bonsai.Endpoint = endpoint
			text := "Beispiel Verwaltung\n" + tt.recipient + "\nMusterweg 1\n12345 Berlin\n20.09.2026\n" + tt.body
			folders := RecipientFolders(cfg, text, []string{"GbR/Steuern", "Privat/Steuern", "Privat/Rechnungen"})
			c := Classify(t.Context(), cfg, text, "scan.pdf", time.Now(), folders)
			if c.Source != "bonsai" || c.RecipientScope != tt.scope || c.SuggestedFolder != tt.folder {
				t.Fatalf("live classification=%+v", c)
			}
			if strings.Contains(tt.body, "Mahnung") && c.DocumentType != "payment-reminder" {
				t.Fatalf("reminder misclassified: %+v", c)
			}
		})
	}
}

func TestBonsaiLiveKnownRecipientAddress(t *testing.T) {
	endpoint := os.Getenv("PAPERLESS_BONSAI_TEST_ENDPOINT")
	if endpoint == "" {
		t.Skip("set PAPERLESS_BONSAI_TEST_ENDPOINT to test a running Bonsai-8B server")
	}
	cfg := config.Default()
	cfg.LLM.Provider = "bonsai"
	cfg.Bonsai.Endpoint = endpoint
	cfg.RecipientAddresses = []string{"Musterstraße 12\n12345 Berlin"}
	cfg.RecipientProfiles = []config.RecipientProfile{{ID: 1, Name: "Alex Example", Scope: "personal", Addresses: cfg.RecipientAddresses, FolderPrefix: "Privat"}}
	text := "Herrn Seller Example\nAndere Straße 9\n54321 Hamburg\n\nAlex Example\nMusterstr. 12\n12345 Berlin\n\n20.09.2026\nMahnung\nIhre private Bestellung eines Sofas, Rechnung 123, ist überfällig. Bitte überweisen Sie EUR 100.\nIhr Ansprechpartner: Robin Contact"
	c := Classify(t.Context(), cfg, text, "scan.pdf", time.Now(), []string{"Privat/Rechnungen", "GbR/Rechnungen"})
	if c.Source != "bonsai" || c.Recipient != "alex-example" || c.RecipientScope != "personal" || c.RecipientProfileID != 1 || c.SuggestedFolder != "Privat/Rechnungen" || c.RecipientAddress == "" {
		t.Fatalf("live address classification=%+v", c)
	}
}

func TestBonsaiClassificationPreservesLocalPoliciesAndContext(t *testing.T) {
	var prompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct{ Messages []struct{ Content string } }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		prompt = request.Messages[1].Content
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": bonsaiTestResponse}, "finish_reason": "stop"}}})
	}))
	defer server.Close()
	cfg := config.Default()
	cfg.LLM.Provider = "bonsai"
	cfg.Bonsai.Endpoint = server.URL
	cfg.RecipientProfiles = []config.RecipientProfile{{Name: "Alex Example", Scope: "personal", Aliases: []string{"A. Example"}, FolderPrefix: "Tax"}}
	text := "Finanzamt\nHerrn Alex Example\n25.02.2026\nEinkommensteuerbescheid"
	var events []progress.Event
	result := ClassifyWithHistory(t.Context(), cfg, text, "scan.pdf", time.Now(), []string{"Tax/2026"}, nil, func(e progress.Event) { events = append(events, e) }, "# Letter\n"+text)
	if result.Source != "bonsai" || result.PhysicalOriginalAction != "keep_original" || result.RecipientScope != "personal" || result.SuggestedFolder != "Tax/2026" {
		t.Fatalf("result=%+v", result)
	}
	if !strings.HasPrefix(result.SuggestedFilename, "2026-02-25__finanzamt__tax-letter") {
		t.Fatalf("filename=%s", result.SuggestedFilename)
	}
	if !strings.Contains(prompt, "# Letter") || !strings.Contains(prompt, "A. Example") {
		t.Fatalf("prompt=%s", prompt)
	}
	for _, e := range events {
		if strings.Contains(e.Message, "Ollama") {
			t.Fatalf("wrong provider: %s", e.Message)
		}
	}
	result = Classify(t.Context(), cfg, strings.Repeat("Grüße 漢字 ", 2000), "long.pdf", time.Now(), []string{"Tax/2026"})
	if !result.ModelContextTruncated || !utf8.ValidString(prompt) {
		t.Fatal("long context not marked for review or invalid UTF8")
	}
}

func TestBonsaiFailuresFallBackToRules(t *testing.T) {
	for _, body := range []string{"not json", "{}", "null", strings.Replace(bonsaiTestResponse, `"personal"`, `"made-up"`, 1), strings.Replace(bonsaiTestResponse, `"Tax/2026"`, `"../../escape"`, 1)} {
		t.Run(body[:min(len(body), 20)], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				payload, _ := json.Marshal(body)
				fmt.Fprintf(w, `{"choices":[{"message":{"content":%s},"finish_reason":"stop"}]}`, payload)
			}))
			defer server.Close()
			cfg := config.Default()
			cfg.LLM.Provider = "bonsai"
			cfg.Bonsai.Endpoint = server.URL
			result := Classify(t.Context(), cfg, strings.Repeat("REWE Kassenbon EUR 12 ", 1000), "scan.pdf", time.Now(), []string{"Tax/2026"})
			if result.Source != "rules" || !result.ModelContextTruncated || !strings.Contains(strings.Join(result.Reasons, " "), "bonsai unavailable or invalid") {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}
