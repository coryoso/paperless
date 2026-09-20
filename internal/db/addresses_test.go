package db

import (
	"path/filepath"
	"testing"

	"paperless/internal/config"
)

func TestRecipientAddressesPersistAndMigrateExistingProfiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.sqlite")
	s, err := Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	profile := config.RecipientProfile{Name: "Alex Example", Scope: "personal", Aliases: []string{"A. Example"}}
	if err := s.SaveRecipientProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	// Recreate the previous schema to exercise upgrading an existing database.
	if _, err := s.conn.ExecContext(t.Context(), `ALTER TABLE recipient_profiles DROP COLUMN addresses; DROP TABLE recipient_addresses; DELETE FROM schema_migrations WHERE version='0005_recipient_addresses.sql'`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := s.RecipientProfiles(t.Context())
	if err != nil || len(profiles) != 1 || profiles[0].Name != profile.Name || len(profiles[0].Aliases) != 1 || len(profiles[0].Addresses) != 0 {
		t.Fatalf("profiles=%+v err=%v", profiles, err)
	}
	profile = profiles[0]
	profile.Addresses = []string{"Musterstraße 12\n12345 Berlin"}
	if err := s.SaveRecipientProfile(t.Context(), profile); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveRecipientAddresses(t.Context(), []string{"Büroweg 3\n54321 Hamburg"}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	profiles, err = s.RecipientProfiles(t.Context())
	if err != nil || len(profiles[0].Addresses) != 1 || profiles[0].Addresses[0] != profile.Addresses[0] {
		t.Fatalf("profile addresses=%+v err=%v", profiles, err)
	}
	if err := s.SaveRecipientAddresses(t.Context(), []string{"12345 Berlin"}); err == nil {
		t.Fatal("accepted a postcode without street/house number")
	}
	addresses, err := s.RecipientAddresses(t.Context())
	if err != nil || len(addresses) != 1 {
		t.Fatalf("invalid save erased addresses: %v %v", addresses, err)
	}
	if err := s.SaveRecipientAddresses(t.Context(), []string{}); err != nil {
		t.Fatal(err)
	}
	addresses, err = s.RecipientAddresses(t.Context())
	if err != nil || len(addresses) != 0 {
		t.Fatalf("addresses not cleared: %v %v", addresses, err)
	}
}
