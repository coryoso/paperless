package db

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"paperless/internal/classify"
	"paperless/internal/db/sqlc"
)

type Approval struct {
	RecipientAddresses []string
	RecipientProfileID int64
	RecipientName      string
	DetectedRecipient  string
	JobID              string
	Sender             string
	Recipient          string
	RecipientScope     string
	Folder             string
	Filename           string
	Weight             float64
}

func (s *Store) LearnApproval(ctx context.Context, approval Approval) error {
	if approval.Folder == "" {
		return nil
	}
	if approval.Weight == 0 {
		approval.Weight = 1
	}
	if approval.RecipientScope == "" {
		approval.RecipientScope = "unknown"
	}
	profiles, err := s.RecipientProfiles(ctx)
	if err != nil {
		return err
	}
	profileKnown := false
	for _, profile := range profiles {
		if profile.Scope != approval.RecipientScope {
			continue
		}
		for _, alias := range append([]string{profile.Name}, profile.Aliases...) {
			if classify.Slug(alias) == classify.Slug(approval.Recipient) {
				profileKnown = true
			}
		}
	}
	now := Now()
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	q := s.Queries.WithTx(tx)
	if err := q.UpsertFolder(ctx, sqlc.UpsertFolderParams{
		Path:      approval.Folder,
		Source:    "learned",
		FirstSeen: now,
		LastSeen:  now,
	}); err != nil {
		return err
	}
	if err := q.IncrementFolderApproval(ctx, sqlc.IncrementFolderApprovalParams{
		LastSeen: now,
		Path:     approval.Folder,
	}); err != nil {
		return err
	}
	if err := q.InsertRoutingExample(ctx, sqlc.InsertRoutingExampleParams{
		CreatedAt:      now,
		SourceJobID:    approval.JobID,
		Sender:         approval.Sender,
		Recipient:      approval.Recipient,
		RecipientScope: approval.RecipientScope,
		Folder:         approval.Folder,
		Filename:       approval.Filename,
		Weight:         approval.Weight,
	}); err != nil {
		return err
	}
	if approval.RecipientProfileID != 0 {
		// Read the latest aliases inside the transaction so concurrent approvals
		// cannot replace each other's newly learned spellings.
		var name, scope, rawAliases string
		if err := tx.QueryRowContext(ctx, `SELECT name,scope,aliases FROM recipient_profiles WHERE id=?`, approval.RecipientProfileID).Scan(&name, &scope, &rawAliases); err != nil {
			return err
		}
		if scope != approval.RecipientScope || classify.Slug(name) != classify.Slug(approval.Recipient) {
			return errors.New("saved recipient changed during approval")
		}
		var aliases []string
		if err := json.Unmarshal([]byte(rawAliases), &aliases); err != nil {
			return err
		}
		alias := strings.TrimSpace(approval.DetectedRecipient)
		known := classify.Slug(alias) == classify.Slug(name)
		for _, a := range aliases {
			known = known || classify.Slug(a) == classify.Slug(alias)
		}
		if !known && LearnableRecipientAlias(alias) {
			aliases = append(aliases, alias)
			encoded, err := json.Marshal(aliases)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE recipient_profiles SET aliases=? WHERE id=?`, string(encoded), approval.RecipientProfileID); err != nil {
				return err
			}
		}
		profileKnown = true
	}
	if !profileKnown && approval.Recipient != "" {
		name := strings.TrimSpace(approval.RecipientName)
		if name == "" {
			name = approval.Recipient
		}
		addresses, err := normalizeProfileAddresses(approval.RecipientAddresses)
		if err != nil {
			return err
		}
		rawAddresses, _ := json.Marshal(addresses)
		if _, err := tx.ExecContext(ctx, `INSERT INTO recipient_profiles (name, scope, addresses) VALUES (?, ?, ?) ON CONFLICT(name, scope) DO NOTHING`, name, approval.RecipientScope, string(rawAddresses)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Empty/generic OCR placeholders do not identify a recipient and cannot be aliases.
func LearnableRecipientAlias(alias string) bool {
	if len(alias) > 200 {
		return false
	}
	switch classify.Slug(alias) {
	case "", "unknown", "unclear", "unbekannt", "herr", "herrn", "frau", "recipient":
		return false
	}
	return true
}
