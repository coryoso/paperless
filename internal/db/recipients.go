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
	rows, err := s.conn.QueryContext(ctx, `SELECT id,name,scope,aliases,folder_prefix FROM recipient_profiles ORDER BY name,scope`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []config.RecipientProfile{}
	for rows.Next() {
		var p config.RecipientProfile
		var aliases string
		if err := rows.Scan(&p.ID, &p.Name, &p.Scope, &aliases, &p.FolderPrefix); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(aliases), &p.Aliases); err != nil {
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
	if p.ID > 0 {
		result, err := s.conn.ExecContext(ctx, `UPDATE recipient_profiles SET name=?,scope=?,aliases=?,folder_prefix=? WHERE id=?`, p.Name, p.Scope, string(aliases), p.FolderPrefix, p.ID)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err == nil && n == 0 {
			return errors.New("recipient profile no longer exists")
		}
		return err
	}
	_, err = s.conn.ExecContext(ctx, `INSERT INTO recipient_profiles (name,scope,aliases,folder_prefix) VALUES (?,?,?,?) ON CONFLICT(name,scope) DO UPDATE SET aliases=excluded.aliases,folder_prefix=excluded.folder_prefix`, p.Name, p.Scope, string(aliases), p.FolderPrefix)
	return err
}
