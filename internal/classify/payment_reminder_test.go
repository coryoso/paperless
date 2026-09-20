package classify

import (
	"strings"
	"testing"
	"time"

	"paperless/internal/config"
)

func TestPaymentReminderDetectionAndModelMerge(t *testing.T) {
	for _, tt := range []struct{ name, body, want string }{
		{"invoice reminder", "Mahnung\nRechnung 123 bleibt offen. Gesamtbetrag EUR 100 inkl. MwSt. Zahlung überfällig.", "payment-reminder"},
		{"tax reminder", "2. Mahnung Grundsteuer B\nFinanzamt\nSteuerforderung EUR 100", "payment-reminder"},
		{"friendly reminder", "Betreff: Freundliche Zahlungserinnerung\nBitte begleichen Sie die überfällige Rechnung.", "payment-reminder"},
		{"English reminder", "Payment reminder: invoice 123\nYour payment is overdue.", "payment-reminder"},
		{"future reminder fees", "Rechnung 123\nBei Zahlungsverzug berechnen wir für jede Mahnung eine Gebühr.", "routine-invoice"},
		{"threatened court order", "Letzte Mahnung\nFalls Sie nicht zahlen, werden wir einen Mahnbescheid beantragen.", "payment-reminder"},
		{"court order", "Amtsgericht\nMahnbescheid\nMahnung zur Rechnung 123", "legal-letter"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Default()
			text := "Merchant\nHerrn Alex Example\nMusterweg 1\n12345 Berlin\n" + tt.body
			base := deterministic(cfg, text, "scan.pdf", time.Now(), []string{"Personal"})
			if base.DocumentType != tt.want {
				t.Fatalf("type=%s, want %s", base.DocumentType, tt.want)
			}
			if tt.want == "payment-reminder" {
				// Invoice/receipt/tax cues must not erase a clear reminder heading.
				for _, modelType := range []string{"routine-invoice", "receipt", "tax-letter"} {
					c := merge(base, Classification{DocumentType: modelType, Confidence: .98}, cfg, text, time.Now(), []string{"Personal"})
					if c.DocumentType != tt.want || !strings.Contains(c.SuggestedFilename, "__payment-reminder__") || c.PhysicalOriginalAction != "review" {
						t.Fatalf("reminder lost during merge: %+v", c)
					}
				}
			}
			if tt.want == "legal-letter" {
				c := merge(base, Classification{DocumentType: "payment-reminder", Confidence: .98}, cfg, text, time.Now(), []string{"Personal"})
				if c.DocumentType != "legal-letter" || !c.Sensitive || c.PhysicalOriginalAction != "keep_original" {
					t.Fatalf("formal court notice lost: %+v", c)
				}
			}
		})
	}
}
