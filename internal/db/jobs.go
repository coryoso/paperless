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
