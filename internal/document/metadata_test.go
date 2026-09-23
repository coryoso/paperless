package document

import (
	"reflect"
	"testing"
)

func TestNormalizeIdentityPreservesSourceAndCanonicalizesPostalText(t *testing.T) {
	raw := Party{Names: []string{"Herrn ALEX EXAMPLE", "Frau ANNA-MARIA MÜLLER", "McDonald"}, Addresses: []string{"EXAMPLE ROAD 12", "12345 EXAMPLE CITY"}, SourceIDs: []int{6}}
	normalized := NormalizeIdentity(raw, true)
	if !reflect.DeepEqual(normalized.Names, []string{"Alex Example", "Anna-Maria Müller", "McDonald"}) {
		t.Fatal(normalized.Names)
	}
	if len(normalized.Addresses) != 1 || !reflect.DeepEqual(normalized.Addresses[0].Lines, []string{"Example Road 12", "12345 Example City"}) {
		t.Fatal(normalized.Addresses)
	}
	if raw.Names[0] != "Herrn ALEX EXAMPLE" || raw.Addresses[0] != "EXAMPLE ROAD 12" {
		t.Fatal("source changed")
	}
	if !reflect.DeepEqual(normalized, NormalizeIdentity(raw, true)) {
		t.Fatal("normalization is not deterministic")
	}
}
func TestNormalizeOrganizationPreservesAcronymsAndMixedCase(t *testing.T) {
	got := NormalizeIdentity(Party{Names: []string{"BMW", "iRobot GmbH", "CITY LIBRARY"}}, false)
	if !reflect.DeepEqual(got.Names, []string{"BMW", "iRobot GmbH", "City Library"}) {
		t.Fatal(got.Names)
	}
}

func TestExplicitAddressesAndNonBodyFields(t *testing.T) {
	u := &Unified{Sender: Party{Addresses: []string{"12345 Exampletown", "Example Street 12a\n54321 Other City"}}, Groups: []Group{{Type: "text", Entries: []Entry{{Content: "body"}}}, {Type: "reference", Entries: []Entry{{Content: "Case A/12", SourceIDs: []int{3}}}}}}
	m := NormalizeMetadata(u)
	if m.Sender.PrimaryAddress != 1 {
		t.Fatalf("complete address should win: %+v", m.Sender)
	}
	a := m.Sender.Addresses[1]
	if a.StreetName != "Example Street" || a.HouseNumber != "12a" || a.PostalCode != "54321" || a.City != "Other City" {
		t.Fatalf("components: %+v", a)
	}
	if len(m.Sender.Addresses) != 2 || len(m.Fields) != 1 || m.Fields[0].Type != "reference" {
		t.Fatalf("evidence lost: %+v", m)
	}
}
