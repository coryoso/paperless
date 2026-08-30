package db

import (
	"context"

	"paperless/internal/db/sqlc"
)

type Approval struct {
	JobID        string
	Sender       string
	Recipient    string
	DocumentType string
	Folder       string
	Filename     string
	Weight       float64
}

func (s *Store) LearnApproval(ctx context.Context, approval Approval) error {
	if approval.Folder == "" {
		return nil
	}
	if approval.Weight == 0 {
		approval.Weight = 1
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
		CreatedAt:    now,
		SourceJobID:  approval.JobID,
		Sender:       approval.Sender,
		Recipient:    approval.Recipient,
		DocumentType: approval.DocumentType,
		Folder:       approval.Folder,
		Filename:     approval.Filename,
		Weight:       approval.Weight,
	}); err != nil {
		return err
	}
	return tx.Commit()
}
