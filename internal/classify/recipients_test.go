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

func TestSavedCanonicalBusinessNameWithoutLegalSuffix(t *testing.T) {
	for _, scope := range []string{"gbr", "organization"} {
		t.Run(scope, func(t *testing.T) {
			cfg := config.Default()
			cfg.LLM.Enabled = false
			cfg.RecipientProfiles = []config.RecipientProfile{{ID: 1, Name: "Example Partners", Scope: scope, FolderPrefix: "Business"}}
			text := "Supplier\n\nExample Partners\nMusterweg 1\n12345 Berlin\n\nRechnung 123"
			folders := []string{"Business/Rechnungen", "Privat/Rechnungen"}
			c := Classify(t.Context(), cfg, text, "scan.pdf", time.Now(), folders)
			if c.RecipientScope != scope || c.RecipientType != "company" || c.RecipientProfileID != 1 {
				t.Fatalf("saved business treated as personal: %+v", c)
			}
			base := deterministic(cfg, text, "scan.pdf", time.Now(), folders)
			c = merge(base, Classification{Recipient: "Example Partners", RecipientType: "company", RecipientScope: scope, SuggestedFolder: folders[0]}, cfg, text, time.Now(), folders)
			if c.RecipientScope != scope || c.SuggestedFolder != folders[0] {
				t.Fatalf("business folder rejected: %+v", c)
			}
			// An exact name shared with a personal profile is still ambiguous.
			cfg.RecipientProfiles = append(cfg.RecipientProfiles, config.RecipientProfile{ID: 2, Name: "Example Partners", Scope: "personal"})
			c = Classify(t.Context(), cfg, text, "scan.pdf", time.Now(), folders)
			if c.RecipientScope != "personal" || !c.RecipientNeedsReview {
				t.Fatalf("shared canonical name established business capacity: %+v", c)
			}
		})
	}
}

func TestGbRContextDistinguishesPartnerFromPrivateRecipient(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Enabled = false
	cfg.RecipientProfiles = []config.RecipientProfile{
		{ID: 1, Name: "Alex Example", Scope: "personal", FolderPrefix: "Privat"},
		{ID: 2, Name: "Example & Partner GbR", Scope: "gbr", Aliases: []string{"Alex Example"}, FolderPrefix: "Business"},
	}
	address := "Finanzamt\nHerrn Alex Example\nMusterweg 1\n12345 Berlin\n"
	for _, tt := range []struct {
		name, text, recipient, scope string
		review                       bool
	}{
		{"partner represents partnership", address + "Alex Example als Empfangsbevollmächtigter der Example & Partner GbR\nUmsatzsteuer 2026", "example-partner-gbr", "gbr", false},
		{"business taxpayer in subject", address + "Steuerschuldner: Example & Partner GbR\nUmsatzsteuer 2026", "example-partner-gbr", "gbr", false},
		{"invoice customer", address + "Leistungsempfänger: Example & Partner GbR\nRechnung 123", "example-partner-gbr", "gbr", false},
		{"personal tax with partnership income", address + "Einkommensteuer 2026\nIhr Gewinnanteil aus Example & Partner GbR", "alex-example", "personal", true},
		{"business topic alone", address + "Umsatzsteuer 2026", "alex-example", "unknown", true},
		{"shared name alone", address + "Rechnung 123", "alex-example", "personal", true},
		{"partnership is sender", "Example & Partner GbR\nHerrn Alex Example\nMusterweg 1\n12345 Berlin\nPrivate Rechnung", "alex-example", "personal", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := Classify(t.Context(), cfg, tt.text, "scan.pdf", time.Now(), []string{"Privat/Steuern", "Business/Steuern"})
			if c.Recipient != tt.recipient || c.RecipientScope != tt.scope || c.RecipientNeedsReview != tt.review {
				t.Fatalf("recipient=%s scope=%s review=%v evidence=%q", c.Recipient, c.RecipientScope, c.RecipientNeedsReview, c.RecipientEvidence)
			}
			folders := RecipientFolders(cfg, tt.text, []string{"Privat/Steuern", "Business/Steuern"})
			if tt.scope == "gbr" && (len(folders) != 1 || folders[0] != "Business/Steuern") {
				t.Fatalf("GbR filing area lost: %v", folders)
			}
			if tt.review && len(folders) != 2 {
				t.Fatalf("prematurely removed candidate folders: %v", folders)
			}
		})
	}
}

func TestGbRAliasAloneCannotEstablishBusinessCapacity(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Enabled = false
	cfg.RecipientProfiles = []config.RecipientProfile{{Name: "Example & Partner GbR", Scope: "gbr", Aliases: []string{"Alex Example"}}}
	c := Classify(t.Context(), cfg, "Herrn Alex Example\nMusterweg 1\n12345 Berlin\nRechnung", "scan.pdf", time.Now(), nil)
	if c.RecipientScope != "personal" || !c.RecipientNeedsReview || c.Recipient != "alex-example" {
		t.Fatalf("alias treated as business evidence: %+v", c)
	}
}

func TestJointRecipientsNeedBusinessContextForGbRWithoutSuffix(t *testing.T) {
	for _, tt := range []struct{ name, names, body, scope, typ string }{
		{"partners joined by und", "Herrn Alex Example und Robin Sample", "Umsatzsteuerbescheid 2026", "gbr", "company"},
		{"partners joined by ampersand", "Herren Alex Example & Robin Sample", "Gesonderte und einheitliche Feststellung 2026", "gbr", "company"},
		{"partners on separate lines", "Herrn\nAlex Example\nRobin Sample", "Gewerbesteuerbescheid 2026", "gbr", "company"},
		{"private couple", "Eheleute Alex Example und Robin Sample", "Einkommensteuerbescheid 2026", "personal", "household"},
		{"personal tax includes business income", "Eheleute Alex Example und Robin Sample", "Einkommensteuerbescheid 2026\nEinkünfte aus Gewerbebetrieb\nGewinnanteil aus Example & Partner GbR", "personal", "household"},
		{"joint invoice lacks business evidence", "Herrn Alex Example und Robin Sample", "Rechnung über Strom für Ihre Wohnung", "personal", "household"},
		{"retail VAT is not business evidence", "Eheleute Alex Example und Robin Sample", "Rechnung\nUmsatzsteuer 19%: EUR 19\nSofa für Ihre Wohnung", "personal", "household"},
		{"VAT rate is not a tax period", "Eheleute Alex Example und Robin Sample", "Rechnung\nUmsatzsteuer 20%: EUR 20\nSofa für Ihre Wohnung", "personal", "household"},
		{"joint business account", "Herrn Alex Example und Robin Sample", "Kontoauszug Geschäftskonto", "gbr", "company"},
		{"single business recipient", "Herrn Alex Example", "Umsatzsteuerbescheid 2026", "unknown", "person"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			text := "Finanzamt\n" + tt.names + "\nMusterweg 1\n12345 Berlin\n" + tt.body
			folders := []string{"Privat/Steuern", "GbR/Steuern"}
			base := deterministic(cfg, text, "scan.pdf", time.Now(), folders)
			if base.RecipientScope != tt.scope || base.RecipientType != tt.typ {
				t.Fatalf("base=%+v", base)
			}
			folder := "Privat/Steuern"
			if tt.scope == "gbr" {
				folder = "GbR/Steuern"
			}
			c := merge(base, Classification{Recipient: base.Recipient, RecipientType: tt.typ, RecipientScope: tt.scope, SuggestedFolder: folder, Confidence: .96}, cfg, text, time.Now(), folders)
			if c.RecipientScope != tt.scope || c.RecipientType != tt.typ {
				t.Fatalf("merge lost context: %+v", c)
			}
			if tt.scope == "gbr" && (c.SuggestedFolder != folder || !c.RecipientNeedsReview) {
				t.Fatalf("inferred partnership should suggest business filing with review: %+v", c)
			}
			if tt.scope == "gbr" {
				candidates := RecipientFolders(cfg, text, folders)
				if len(candidates) != 1 || candidates[0] != folder {
					t.Fatalf("joint business offered personal filing: %v", candidates)
				}
			}
			if tt.scope == "personal" && c.SuggestedFolder != folder {
				t.Fatalf("household should retain private filing: %+v", c)
			}
		})
	}
}

func TestRecipientFolderCorrectionPreservesCompatibleModelAlternative(t *testing.T) {
	c := Classification{Recipient: "alex-example-und-robin-sample", RecipientScope: "gbr", SuggestedFolder: "Privat/Steuern", FolderRankings: []FolderRanking{{Folder: "Privat/Steuern", Confidence: .96}, {Folder: "Business/Steuern", Confidence: .94}}}
	validateRecipientFolder(&c, config.Default())
	if c.SuggestedFolder != "Business/Steuern" || !c.RecipientNeedsReview || len(c.FolderRankings) != 1 {
		t.Fatalf("compatible alternative lost: %+v", c)
	}
	if FolderFitsRecipient("Business/Steuern", Classification{RecipientScope: "personal"}, nil) {
		t.Fatal("personal recipient accepted in a business folder")
	}
}

func TestApprovedRoutingIsScopedAndRespectsProfileBoundaries(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Enabled = false
	cfg.RecipientProfiles = []config.RecipientProfile{{Name: "Alex Example", Scope: "sole_proprietor", FolderPrefix: "Business"}}
	text := "Merchant\nHerrn Alex Example\nMusterweg 1\n12345 Berlin\nRechnung 12.03.2026"
	history := []RoutingExample{
		{Sender: "merchant", Recipient: "alex-example", RecipientScope: "sole_proprietor", Folder: "Business", Approvals: 20},
		{Sender: "merchant", Recipient: "alex-example", RecipientScope: "personal", Folder: "Misc/Alpha", Approvals: 2},
	}
	c := ClassifyWithHistory(t.Context(), cfg, text, "scan.pdf", time.Now(), []string{"Misc/Alpha", "Business"}, history, nil)
	if c.SuggestedFolder != "Misc/Alpha" || !strings.Contains(strings.Join(c.Reasons, " "), "learned from approvals") {
		t.Fatalf("routing=%+v", c)
	}
	history = append(history, RoutingExample{Sender: "merchant", Recipient: "alex-example", RecipientScope: "personal", Folder: "Misc/Beta", Approvals: 2})
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
		c := Classification{Sender: "Merchant", Recipient: "Alex Example", RecipientType: "person", RecipientScope: "personal", RecipientEvidence: "Alex Example", DocumentDate: "2026-09-06", SuggestedFolder: "Personal", SuggestedFilename: "2026-09-06__merchant__routine-invoice__service.pdf", Summary: "Service invoice", Confidence: .95}
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
