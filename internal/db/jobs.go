package db

import (
	"context"
	"database/sql"
	"errors"
	"paperless/internal/db/sqlc"
)

// DeleteJob removes a job and every database record owned by it atomically.
func (s *Store) DeleteJob(ctx context.Context, jobID string) error {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	q := s.Queries.WithTx(tx)
	if err := q.DeleteJobEvents(ctx, jobID); err != nil {
		return err
	}
	if err := q.DeleteJobRoutingExamples(ctx, jobID); err != nil {
		return err
	}
	if err := q.DeleteJob(ctx, jobID); err != nil {
		return err
	}
	return tx.Commit()
}

// RegisterRawCopy publishes a hash and elects the original in one transaction.
// Concurrent identical uploads must not both decide they duplicate each other.
func (s *Store) RegisterRawCopy(ctx context.Context, params sqlc.SetRawCopyParams) (sqlc.Job, error) {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return sqlc.Job{}, err
	}
	defer tx.Rollback()
	q := s.Queries.WithTx(tx)
	if err := q.SetRawCopy(ctx, params); err != nil {
		return sqlc.Job{}, err
	}
	duplicate, findErr := q.FindDuplicateByHash(ctx, sqlc.FindDuplicateByHashParams{FileHash: params.FileHash, ID: params.ID})
	if findErr != nil && !errors.Is(findErr, sql.ErrNoRows) {
		return sqlc.Job{}, findErr
	}
	if err := tx.Commit(); err != nil {
		return sqlc.Job{}, err
	}
	return duplicate, findErr
}

// ResetJobForReprocessing clears derived results but keeps document identity,
// its original source, scan timestamp, and any existing archived file reference.
func (s *Store) ResetJobForReprocessing(ctx context.Context, jobID, inputPath string) error {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET current_path=?, status='received',
 text_path='', text_hash='', page_count=0, input_kind='', text_source='',
 summary='', classification_json='', confidence=0, physical_original_action='',
 error='', duplicate_of='', manual_override=0, updated_at=? WHERE id=?`, inputPath, Now(), jobID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM embedding_chunks WHERE job_id=?`, jobID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM document_embeddings WHERE job_id=?`, jobID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM document_blocks WHERE job_id=?`, jobID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}
