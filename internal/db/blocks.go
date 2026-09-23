package db

import (
	"context"
	"encoding/json"
	"paperless/internal/document"
)

func (s *Store) DocumentBlocks(ctx context.Context, jobID string) (document.Document, error) {
	var d document.Document
	var raw, unified string
	err := s.conn.QueryRowContext(ctx, `SELECT source_hash,version,distance_multiplier,blocks_json,unified_json FROM document_blocks WHERE job_id=?`, jobID).Scan(&d.SourceHash, &d.Version, &d.DistanceMultiplier, &raw, &unified)
	if err != nil {
		return d, err
	}
	if err = json.Unmarshal([]byte(unified), &d.Unified); err != nil {
		return d, err
	}
	err = json.Unmarshal([]byte(raw), &d.Blocks)
	return d, err
}

// SaveDocumentBlocks invalidates the old index in the same transaction as the
// canonical JSON update, so old similarity evidence cannot outlive a saved edit.
func (s *Store) SaveDocumentBlocks(ctx context.Context, jobID string, d document.Document) error {
	raw, err := json.Marshal(d.Blocks)
	if err != nil {
		return err
	}
	unified, err := json.Marshal(d.Unified)
	if err != nil {
		return err
	}
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO document_blocks(job_id,source_hash,version,distance_multiplier,blocks_json,unified_json,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(job_id) DO UPDATE SET source_hash=excluded.source_hash,version=excluded.version,distance_multiplier=excluded.distance_multiplier,blocks_json=excluded.blocks_json,unified_json=excluded.unified_json,updated_at=excluded.updated_at`, jobID, d.SourceHash, d.Version, d.DistanceMultiplier, string(raw), string(unified), Now())
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM embedding_chunks WHERE job_id=?`, jobID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM document_embeddings WHERE job_id=?`, jobID); err != nil {
		return err
	}
	return tx.Commit()
}
