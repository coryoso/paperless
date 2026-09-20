package classify

import (
	"strings"
	"testing"
	"time"

	"paperless/internal/config"
)

func TestAddressBlocksPrioritizeKnownDeliveryLocation(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Enabled = false
	cfg.RecipientAddresses = []string{"Musterstraße 12a, 12345 Berlin"}
	text := "Herrn Seller Example\nAndere Straße 9\n54321 Hamburg\n\nAlex Example\nMusterstr. 12 a\n12345 Berlin\n\nIhr Ansprechpartner: Robin Contact\nMahnung"
	c := Classify(t.Context(), cfg, text, "scan.pdf", time.Now(), []string{"Personal"})
	if c.Recipient != "alex-example" || c.RecipientScope != "personal" || !strings.Contains(c.RecipientAddress, "Musterstr.") {
		t.Fatalf("wrong postal recipient: %+v", c)
	}
	if !strings.Contains(c.RecipientEvidence, "12345 Berlin") || strings.Contains(c.RecipientEvidence, "Seller") {
		t.Fatalf("wrong address evidence: %q", c.RecipientEvidence)
	}
	base := deterministic(cfg, text, "scan.pdf", time.Now(), nil)
	merged := merge(base, Classification{Recipient: "Seller Example", RecipientType: "person", RecipientScope: "personal"}, cfg, text, time.Now(), nil)
	if merged.Recipient != "alex-example" || !merged.RecipientNeedsReview {
		t.Fatalf("model replaced the known addressee with the sender: %+v", merged)
	}
}

func TestPersonaAddressesResolveOnlyMatchingNames(t *testing.T) {
	for _, tt := range []struct {
		name, addressee, body, scope string
		id                           int64
		review                       bool
	}{
		{"business persona", "Alex Example", "Rechnung für Beratung", "gbr", 2, true},
		{"personal tax at work", "Alex Example", "Einkommensteuerbescheid 2026", "personal", 1, true},
		{"different person at same address", "Robin Sample", "Rechnung", "personal", 0, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.LLM.Enabled = false
			cfg.RecipientProfiles = []config.RecipientProfile{
				{ID: 1, Name: "Alex Example", Scope: "personal", Addresses: []string{"Privatweg 1\n12345 Berlin"}},
				{ID: 2, Name: "Example Partners", Scope: "gbr", Aliases: []string{"Alex Example"}, Addresses: []string{"Bürostraße 8\n12345 Berlin"}},
			}
			text := "Supplier\n\n" + tt.addressee + "\nBürostr. 8\n12345 Berlin\n\n" + tt.body
			c := Classify(t.Context(), cfg, text, "scan.pdf", time.Now(), nil)
			if c.RecipientScope != tt.scope || c.RecipientProfileID != tt.id || c.RecipientNeedsReview != tt.review {
				t.Fatalf("association=%+v", c)
			}
			if tt.id == 0 && c.Recipient != "robin-sample" {
				t.Fatal("address alone substituted a saved person's identity")
			}
		})
	}
}

func TestSharedAddressDoesNotChooseBetweenPersonas(t *testing.T) {
	cfg := config.Default()
	cfg.LLM.Enabled = false
	address := "Musterweg 1\n12345 Berlin"
	cfg.RecipientProfiles = []config.RecipientProfile{
		{ID: 1, Name: "Alex Example", Scope: "personal", Addresses: []string{address}},
		{ID: 2, Name: "Example Partners", Scope: "gbr", Aliases: []string{"Alex Example"}, Addresses: []string{address}},
	}
	c := Classify(t.Context(), cfg, "Herrn Alex Example\n"+address+"\nRechnung", "scan.pdf", time.Now(), nil)
	if c.RecipientScope != "personal" || !c.RecipientNeedsReview {
		t.Fatalf("shared address decided business capacity: %+v", c)
	}
}

func TestAddressMatchingRequiresStreetHouseAndPostcode(t *testing.T) {
	key := addressKey("Musterstraße 12a\n12345 Berlin")
	for _, tt := range []struct {
		address string
		match   bool
	}{
		{"Musterstr. 12 a, D-12345 Berlin", true},
		{"Musterstraße 12b\n12345 Berlin", false},
		{"Andere Straße 12a\n12345 Berlin", false},
		{"Musterstraße 12a\n54321 Hamburg", false},
		{"12345 Berlin", false},
		{"Kundennummer 12345", false},
	} {
		if got := addressKey(tt.address); (got == key) != tt.match {
			t.Errorf("address=%q key=%q expected match=%v", tt.address, got, tt.match)
		}
	}
}

func TestCareOfContactDoesNotReplacePostalRecipient(t *testing.T) {
	for _, tt := range []struct{ lines, name, scope string }{
		{"Example Partners GbR\nz. Hd. Alex Example", "example-partners-gbr", "gbr"},
		{"Alex Example\nc/o Example Partners GbR", "alex-example", "personal"},
		{"Alex Example\nc/o\nExample Partners GbR", "alex-example", "personal"},
	} {
		cfg := config.Default()
		cfg.LLM.Enabled = false
		cfg.RecipientAddresses = []string{"Musterweg 1\n12345 Berlin"}
		c := Classify(t.Context(), cfg, "Supplier\n\n"+tt.lines+"\nMusterweg 1\n12345 Berlin\n\nRechnung", "scan.pdf", time.Now(), nil)
		if c.Recipient != tt.name || c.RecipientScope != tt.scope {
			t.Fatalf("%q: recipient=%s scope=%s evidence=%q", tt.lines, c.Recipient, c.RecipientScope, c.RecipientEvidence)
		}
	}
}

func TestEquallyPlausibleAddressesAllowGroundedModelRefinement(t *testing.T) {
	cfg := config.Default()
	text := "Seller Example\nSenderweg 1\n54321 Hamburg\n\nAlex Example\nMusterweg 2\n12345 Berlin\n\nRechnung"
	base := deterministic(cfg, text, "scan.pdf", time.Now(), nil)
	c := merge(base, Classification{Recipient: "Alex Example", RecipientType: "person", RecipientScope: "personal"}, cfg, text, time.Now(), nil)
	if c.Recipient != "alex-example" || !strings.Contains(c.RecipientAddress, "Musterweg") || !c.RecipientNeedsReview {
		t.Fatalf("ambiguous address refinement=%+v", c)
	}
}

func TestAddressResolvedAliasSurvivesModelMerge(t *testing.T) {
	for _, tt := range []struct{ name, scope, folder, typ string }{
		{"Example Partners", "gbr", "GbR/Rechnungen", "company"},
		{"Alexandra Example", "personal", "Privat/Rechnungen", "person"},
	} {
		for _, modelRecipient := range []string{"Alex Example", "", "Seller Example"} {
			t.Run(tt.scope+"/"+modelRecipient, func(t *testing.T) {
				cfg := config.Default()
				cfg.RecipientProfiles = []config.RecipientProfile{{ID: 2, Name: tt.name, Scope: tt.scope, Aliases: []string{"Alex Example"}, Addresses: []string{"Bürostraße 8\n12345 Berlin"}, FolderPrefix: strings.Split(tt.folder, "/")[0]}}
				text := "Seller Example\nSenderweg 1\n54321 Hamburg\n\nAlex Example\nBürostr. 8\n12345 Berlin\n\nRechnung für Beratung"
				folders := []string{tt.folder}
				base := deterministic(cfg, text, "scan.pdf", time.Now(), folders)
				if base.Recipient != Slug(tt.name) || base.RecipientProfileID != 2 {
					t.Fatalf("address association not established: %+v", base)
				}
				c := PreferSavedRecipient(merge(base, Classification{Recipient: modelRecipient, RecipientType: "person", SuggestedFolder: tt.folder}, cfg, text, time.Now(), folders), cfg)
				if c.Recipient != Slug(tt.name) || c.DetectedRecipient != "alex-example" || c.RecipientProfileID != 2 || c.RecipientScope != tt.scope || c.RecipientType != tt.typ || c.SuggestedFolder != tt.folder || !strings.Contains(c.RecipientAddress, "Bürostr.") || !strings.Contains(c.RecipientEvidence, "Alex Example") {
					t.Fatalf("address association lost in merge: %+v", c)
				}
				if (tt.scope == "gbr" || modelRecipient == "Seller Example") && !c.RecipientNeedsReview {
					t.Fatalf("ambiguous capacity or conflicting model must require review: %+v", c)
				}
			})
		}
	}
}
