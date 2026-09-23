package document

import "testing"

func TestConsolidateFragmentsAndPreserveConflicts(t *testing.T) {
	blocks := []Block{
		{ID: 1, Type: "sender", Content: "Deutsche\nRentenversicherung"},
		{ID: 2, Type: "sender", Content: "Berlin-Brandenburg"},
		{ID: 3, Type: "sender", Content: "Deutsche Rentenversicherung Berlin-Brandenburg, 15228 Frankfurt (Oder)"},
		{ID: 4, Type: "reference", Content: "ABC 123"},
		{ID: 5, Type: "reference", Content: "ABC 456"},
		{ID: 6, Type: "text", Content: "Keep this paragraph."},
		{ID: 7, Type: "text", Content: "Keep this paragraph. And this addition."},
	}
	u := Consolidate(blocks)
	if len(u.Groups) != 3 || len(u.Groups[0].Entries) != 1 || len(u.Groups[0].Entries[0].SourceIDs) != 3 {
		t.Fatalf("lost merged provenance: %+v", u)
	}
	if len(u.Groups[1].Entries) != 2 || len(u.Groups[2].Entries) != 2 {
		t.Fatal("conflicts/body boundaries were removed", u)
	}
	if blocks[0].Content != "Deutsche\nRentenversicherung" {
		t.Fatal("original blocks mutated")
	}
}

func TestConsolidationPreservesContactsAndSignedAmounts(t *testing.T) {
	blocks := []Block{{ID: 1, Type: "sender", Content: "North District"}, {ID: 2, Type: "sender", Content: "info@north-district.example"}, {ID: 3, Type: "sender", Content: "City Library North District, 12345 Example"}, {ID: 4, Type: "payment", Content: "-100 EUR"}, {ID: 5, Type: "payment", Content: "100 EUR"}}
	u := Consolidate(blocks)
	if len(u.Groups[0].Entries) != 2 || len(u.Groups[1].Entries) != 2 {
		t.Fatal(u)
	}
	for _, e := range u.Groups[0].Entries {
		if e.Content == "info@north-district.example" && len(e.SourceIDs) != 1 {
			t.Fatal("name merged into contact", e)
		}
	}
}
