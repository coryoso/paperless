package classify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"paperless/internal/config"
)

func TestRecipientCapacityUsesAddresseeNotSenderOrTaxTopic(t *testing.T) {
	cases := []struct{ name, text, recipient, scope string }{
		{"personal tax reminder", "Gemeinde Beispiel\nHerrn\nAlex Example\nMusterweg 1\n12345 Berlin\nMahnung Grundsteuer B", "alex-example", "personal"},
		{"GbR addressee", "Finanzamt\nFirma Example & Partner GbR\nMusterweg 1\n12345 Berlin\nMahnung", "example-partner-gbr", "gbr"},
		{"GbR without title", "Finanzamt\nExample & Partner GbR\nMusterweg 1\n12345 Berlin\nMahnung", "example-partner-gbr", "gbr"},
		{"individual business", "Finanzamt\nHerrn Alex Example\nEinzelunternehmer\nMusterweg 1\n12345 Berlin\nUmsatzsteuer", "alex-example-einzelunternehmer", "sole_proprietor"},
		{"ambiguous individual", "Finanzamt\nHerrn Alex Example\nMusterweg 1\n12345 Berlin\nUmsatzsteuer", "alex-example", "unknown"},
		{"GbR sender", "Example & Partner GbR\nHerrn Alex Example\nMusterweg 1\n12345 Berlin\nRechnung", "alex-example", "personal"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.LLM.Enabled = false
			c := Classify(t.Context(), cfg, tt.text, "scan.pdf", time.Now(), []string{"Example & Partner GbR/Steuern", "Privat/Steuern"})
			if c.Recipient != tt.recipient || c.RecipientScope != tt.scope {
				t.Fatalf("recipient=%q scope=%q evidence=%q", c.Recipient, c.RecipientScope, c.RecipientEvidence)
			}
			if c.RecipientScope == "unknown" && !c.RecipientNeedsReview {
				t.Fatal("unknown capacity must need review")
			}
		})
	}
}

func TestMergeRejectsPersonalRecipientInGbRFolder(t *testing.T) {
	cfg := config.Default()
	text := "Gemeinde Beispiel\nHerrn\nAlex Example\nMusterweg 1\n12345 Berlin\nMahnung Grundsteuer B"
	folders := []string{"Example & Partner GbR/Steuern", "Privat/Steuern"}
	base := deterministic(cfg, text, "scan.pdf", time.Now(), folders)
	c := merge(base, Classification{Recipient: "Alex Example", RecipientType: "person", RecipientScope: "gbr", SuggestedFolder: folders[0], FolderRankings: []FolderRanking{{Folder: folders[0]}}, Confidence: .98}, cfg, text, time.Now(), folders)
	if c.RecipientScope != "personal" || c.SuggestedFolder != "" || len(c.FolderRankings) != 0 {
		t.Fatalf("cross-capacity suggestion survived: %+v", c)
	}
}

func TestModelCanRefineColumnContaminatedOCRRecipient(t *testing.T) {
	text := "Herrn schaften\nAlex Example Gemeindekasse\nMusterweg 1 Standort Rathaus\n12345 Berlin"
	cfg := config.Default()
	base := deterministic(cfg, text, "scan.pdf", time.Now(), nil)
	c := merge(base, Classification{Recipient: "Alex Example", RecipientType: "person", RecipientScope: "personal"}, cfg, text, time.Now(), nil)
	if c.Recipient != "alex-example" || c.RecipientScope != "personal" {
		t.Fatalf("recipient=%+v", c)
	}
	c = merge(base, Classification{Recipient: "Invented Person", RecipientType: "person"}, cfg, text, time.Now(), nil)
	if c.Recipient == "invented-person" || !c.RecipientNeedsReview {
		t.Fatalf("invented recipient accepted: %+v", c)
	}
}

func TestSamePersonBusinessProfileDoesNotMakePersonalMailBusiness(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Enabled = false
	cfg.RecipientProfiles = []config.RecipientProfile{{Name: "Alex Example", Scope: "sole_proprietor"}}
	c := Classify(t.Context(), cfg, "Herrn Alex Example\nMusterweg 1\n12345 Berlin\nGrundsteuer B", "scan.pdf", time.Now(), nil)
	if c.RecipientScope != "personal" || !c.RecipientNeedsReview {
		t.Fatalf("shared name must need capacity review: %+v", c)
	}
}

func TestApprovedRoutingIsScopedAndRespectsProfileBoundaries(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Enabled = false
	cfg.RecipientProfiles = []config.RecipientProfile{{Name: "Alex Example", Scope: "sole_proprietor", FolderPrefix: "Business"}}
	text := "Merchant\nHerrn Alex Example\nMusterweg 1\n12345 Berlin\nRechnung 12.03.2026"
	history := []RoutingExample{
		{Sender: "merchant", Recipient: "alex-example", RecipientScope: "sole_proprietor", DocumentType: "routine-invoice", Folder: "Business", Approvals: 20},
		{Sender: "merchant", Recipient: "alex-example", RecipientScope: "personal", DocumentType: "routine-invoice", Folder: "Misc/Alpha", Approvals: 2},
	}
	c := ClassifyWithHistory(t.Context(), cfg, text, "scan.pdf", time.Now(), []string{"Misc/Alpha", "Business"}, history, nil)
	if c.SuggestedFolder != "Misc/Alpha" || !strings.Contains(strings.Join(c.Reasons, " "), "learned from approvals") {
		t.Fatalf("routing=%+v", c)
	}
	history = append(history, RoutingExample{Sender: "merchant", Recipient: "alex-example", RecipientScope: "personal", DocumentType: "routine-invoice", Folder: "Misc/Beta", Approvals: 2})
	c = ClassifyWithHistory(t.Context(), cfg, text, "scan.pdf", time.Now(), []string{"Misc/Alpha", "Misc/Beta", "Business"}, history, nil)
	if c.SuggestedFolder != "" {
		t.Fatalf("conflicting history must require destination review: %+v", c)
	}
}

func TestSavedRecipientsResolveAliasesWithoutCrossingCapacities(t *testing.T) {
	cfg := config.Default()
	cfg.RecipientProfiles = []config.RecipientProfile{{ID: 1, Name: "Alex Example", Scope: "personal", Aliases: []string{"A. Example"}}, {ID: 2, Name: "Example Consulting", Scope: "sole_proprietor", Aliases: []string{"A. Example"}}}
	c := PreferSavedRecipient(Classification{Recipient: "a-example", RecipientScope: "personal"}, cfg)
	if c.RecipientProfileID != 1 || c.Recipient != "alex-example" || c.DetectedRecipient != "a-example" {
		t.Fatalf("saved alias not resolved: %+v", c)
	}
	c = PreferSavedRecipient(Classification{Recipient: "a-example", RecipientScope: "unknown"}, cfg)
	if c.RecipientProfileID != 0 {
		t.Fatal("ambiguous capacity selected arbitrary saved identity")
	}
	cfg.RecipientProfiles = append(cfg.RecipientProfiles, config.RecipientProfile{ID: 3, Name: "Another Example", Scope: "personal", Aliases: []string{"A. Example"}})
	c = PreferSavedRecipient(Classification{Recipient: "a-example", RecipientScope: "personal"}, cfg)
	if c.RecipientProfileID != 0 || !c.RecipientNeedsReview {
		t.Fatal("ambiguous alias selected arbitrary saved identity")
	}
}

func TestClassificationSendsLayoutToModelAndGroundsAgainstReadingText(t *testing.T) {
	markdown := "## Merchant\n\nHerrn Alex Example\n\n| Item | EUR |\n| --- | --- |\n| Service | 25,00 |"
	requests := []ollamaChatRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/tags" {
			json.NewEncoder(w).Encode(map[string]any{"models": []map[string]string{{"name": "qwen3.5:9b-q4_K_M"}}})
			return
		}
		var request ollamaChatRequest
		json.NewDecoder(r.Body).Decode(&request)
		requests = append(requests, request)
		c := Classification{DocumentType: "routine-invoice", Sender: "Merchant", Recipient: "Alex Example", RecipientType: "person", RecipientScope: "personal", RecipientEvidence: "Alex Example", DocumentDate: "2026-09-06", SuggestedFolder: "Personal", SuggestedFilename: "2026-09-06__merchant__routine-invoice__service.pdf", Summary: "Service invoice", Confidence: .95}
		content, _ := json.Marshal(c)
		json.NewEncoder(w).Encode(ollamaChatResponse{Message: ollamaMessage{Content: string(content)}})
	}))
	defer server.Close()
	cfg := config.Default()
	cfg.LLM.Endpoint = server.URL
	cfg.LLM.Enabled = true
	c := ClassifyWithHistory(t.Context(), cfg, "Merchant\nHerrn Alex Example\nMusterweg 1\n12345 Berlin\nRechnung 06.09.2026", "scan.pdf", time.Now(), []string{"Personal"}, nil, nil, markdown)
	if len(requests) != 2 {
		t.Fatalf("model calls=%d", len(requests))
	}
	for _, request := range requests {
		if !strings.Contains(request.Messages[0].Content, markdown) {
			t.Fatal("model did not receive Markdown")
		}
	}
	if c.Recipient != "alex-example" || c.RecipientScope != "personal" || c.Source != "ollama" {
		t.Fatalf("layout broke grounding: %+v", c)
	}
}
