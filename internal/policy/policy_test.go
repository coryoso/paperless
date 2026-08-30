package policy

import (
	"context"
	"database/sql"
	"testing"

	"paperless/internal/classify"
	"paperless/internal/config"
	"paperless/internal/db/sqlc"
)

type fakeCounter struct {
	examples int64
	folderOK bool
}

func (f fakeCounter) ApprovedExampleCount(context.Context, sqlc.ApprovedExampleCountParams) (int64, error) {
	return f.examples, nil
}

func (f fakeCounter) GetFolderApprovedCount(context.Context, string) (int64, error) {
	if !f.folderOK {
		return 0, sql.ErrNoRows
	}
	return 1, nil
}

func TestPolicyRequiresLearnedExamples(t *testing.T) {
	cfg := config.Default()
	c := classify.Classification{
		DocumentType:           "receipt",
		Sender:                 "rewe",
		SuggestedFolder:        "Receipts/Groceries",
		PhysicalOriginalAction: "discard_candidate",
		Confidence:             0.96,
	}
	decision := Evaluate(t.Context(), cfg, fakeCounter{examples: 0, folderOK: true}, c)
	if decision.AutoFile {
		t.Fatal("expected auto-file to be blocked before learning")
	}
	decision = Evaluate(t.Context(), cfg, fakeCounter{examples: 2, folderOK: true}, c)
	if !decision.AutoFile {
		t.Fatalf("expected auto-file after learning, reasons: %v", decision.Reasons)
	}
}

func TestPolicyBlocksSensitiveDocuments(t *testing.T) {
	cfg := config.Default()
	c := classify.Classification{
		DocumentType:           "tax-letter",
		Sender:                 "finanzamt",
		SuggestedFolder:        "Admin/Tax",
		PhysicalOriginalAction: "keep_original",
		Confidence:             0.99,
		Sensitive:              true,
	}
	decision := Evaluate(t.Context(), cfg, fakeCounter{examples: 10, folderOK: true}, c)
	if decision.AutoFile {
		t.Fatal("expected sensitive document to be blocked")
	}
}
