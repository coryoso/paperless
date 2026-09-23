package db

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"paperless/internal/classify"
	"paperless/internal/config"
)

func (s *Store) RecipientProfiles(ctx context.Context) ([]config.RecipientProfile, error) {
	rows, err := s.conn.QueryContext(ctx, `SELECT id,name,scope,aliases,folder_prefix,addresses FROM recipient_profiles ORDER BY name,scope`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []config.RecipientProfile{}
	for rows.Next() {
		var p config.RecipientProfile
		var aliases, addresses string
		if err := rows.Scan(&p.ID, &p.Name, &p.Scope, &aliases, &p.FolderPrefix, &addresses); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(aliases), &p.Aliases); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(addresses), &p.Addresses); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) SaveRecipientProfile(ctx context.Context, p config.RecipientProfile) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len(p.Name) > 200 || !classify.ValidRecipientScope(p.Scope) {
		return errors.New("a recipient name and valid capacity are required")
	}
	for _, a := range p.Aliases {
		if len(a) > 200 {
			return errors.New("recipient alias is too long")
		}
	}
	if p.Aliases == nil {
		p.Aliases = []string{}
	}
	aliases, err := json.Marshal(p.Aliases)
	if err != nil {
		return err
	}
	p.Addresses, err = normalizeProfileAddresses(p.Addresses)
	if err != nil {
		return err
	}
	addresses, err := json.Marshal(p.Addresses)
	if err != nil {
		return err
	}
	if p.ID > 0 {
		result, err := s.conn.ExecContext(ctx, `UPDATE recipient_profiles SET name=?,scope=?,aliases=?,folder_prefix=?,addresses=? WHERE id=?`, p.Name, p.Scope, string(aliases), p.FolderPrefix, string(addresses), p.ID)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err == nil && n == 0 {
			return errors.New("recipient profile no longer exists")
		}
		return err
	}
	_, err = s.conn.ExecContext(ctx, `INSERT INTO recipient_profiles (name,scope,aliases,folder_prefix,addresses) VALUES (?,?,?,?,?) ON CONFLICT(name,scope) DO UPDATE SET aliases=excluded.aliases,folder_prefix=excluded.folder_prefix,addresses=excluded.addresses`, p.Name, p.Scope, string(aliases), p.FolderPrefix, string(addresses))
	return err
}

func normalizeAddresses(addresses []string) ([]string, error) {
	if len(addresses) > 30 {
		return nil, errors.New("save at most 30 addresses")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, address := range addresses {
		address = strings.TrimSpace(address)
		if address == "" {
			continue
		}
		if len(address) > 500 || !classify.ValidRecipientAddress(address) {
			return nil, errors.New("each address needs a street and house number, followed by a 4–5 digit postcode and city")
		}
		if !seen[address] {
			out = append(out, address)
			seen[address] = true
		}
	}
	return out, nil
}

func (s *Store) RecipientAddresses(ctx context.Context) ([]string, error) {
	rows, err := s.conn.QueryContext(ctx, `SELECT address FROM recipient_addresses ORDER BY address`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	addresses := []string{}
	for rows.Next() {
		var address string
		if err := rows.Scan(&address); err != nil {
			return nil, err
		}
		addresses = append(addresses, address)
	}
	return addresses, rows.Err()
}

func (s *Store) SaveRecipientAddresses(ctx context.Context, addresses []string) error {
	addresses, err := normalizeAddresses(addresses)
	if err != nil {
		return err
	}
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM recipient_addresses`); err != nil {
		return err
	}
	for _, address := range addresses {
		if _, err := tx.ExecContext(ctx, `INSERT INTO recipient_addresses (address) VALUES (?)`, address); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Profile storage preserves user-confirmed partial/international addresses.
// Routing still requires ValidRecipientAddress before matching a delivery place.
func normalizeProfileAddresses(addresses []string) ([]string, error) {
	if len(addresses) > 30 {
		return nil, errors.New("save at most 30 addresses")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, value := range addresses {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if len(value) > 2000 {
			return nil, errors.New("recipient address is too long")
		}
		if !seen[value] {
			out = append(out, value)
			seen[value] = true
		}
	}
	return out, nil
}
