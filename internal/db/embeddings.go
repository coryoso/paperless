package db

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
)

type EmbeddingMatch struct {
	JobID          string  `json:"job_id"`
	Filename       string  `json:"filename"`
	Folder         string  `json:"folder"`
	Recipient      string  `json:"recipient"`
	RecipientScope string  `json:"recipient_scope"`
	DocumentType   string  `json:"document_type"`
	Excerpt        string  `json:"excerpt"`
	QueryExcerpt   string  `json:"query_excerpt"`
	Distance       float64 `json:"-"`
}

func (s *Store) EmbeddingCurrent(ctx context.Context, jobID, hash, model string) (bool, error) {
	var count int
	err := s.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM document_embeddings WHERE job_id=? AND content_hash=? AND model_key=?`, jobID, hash, model).Scan(&count)
	return count == 1, err
}

func (s *Store) DeleteEmbedding(ctx context.Context, jobID string) error {
	_, err := s.conn.ExecContext(ctx, `DELETE FROM document_embeddings WHERE job_id=?`, jobID)
	return err
}

func (s *Store) SaveEmbedding(ctx context.Context, jobID, hash, model string, chunks []string, vectors [][]float32) error {
	if len(chunks) == 0 || len(chunks) != len(vectors) || len(vectors[0]) == 0 || len(vectors[0]) > 4096 {
		return errors.New("invalid document embeddings")
	}
	dimension := len(vectors[0])
	for _, vector := range vectors {
		if len(vector) != dimension {
			return errors.New("inconsistent document embeddings")
		}
	}
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM document_embeddings WHERE job_id=?`, jobID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO document_embeddings(job_id,content_hash,model_key,dimensions,indexed_at) VALUES(?,?,?,?,?)`, jobID, hash, model, dimension, Now()); err != nil {
		return err
	}
	for i, vector := range vectors {
		data := make([]byte, len(vector)*4)
		var norm float64
		for n, v := range vector {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return errors.New("invalid vector")
			}
			norm += float64(v) * float64(v)
			binary.LittleEndian.PutUint32(data[n*4:], math.Float32bits(v))
		}
		if norm == 0 {
			return errors.New("empty vector")
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO embedding_chunks(job_id,ordinal,excerpt,vector) VALUES(?,?,?,?)`, jobID, i, chunks[i], data); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Exact cosine search keeps model dimensions flexible. Only human-approved,
// archived documents are eligible; automatic filing is not a teaching example.
// Rank sections first, then return each document once with its best evidence.
func (s *Store) SimilarDocuments(ctx context.Context, jobID, model string) ([]EmbeddingMatch, error) {
	rows, err := s.conn.QueryContext(ctx, `WITH candidates AS (
 SELECT c.job_id, r.filename, r.folder, r.recipient, r.recipient_scope, r.document_type,
 c.excerpt, q.excerpt AS query_excerpt,
 vec_distance_cosine(c.vector,q.vector) AS distance
 FROM document_embeddings source
 JOIN embedding_chunks q ON q.job_id=source.job_id
 JOIN document_embeddings target ON target.model_key=source.model_key AND target.dimensions=source.dimensions AND target.job_id<>source.job_id
 JOIN embedding_chunks c ON c.job_id=target.job_id
 JOIN jobs j ON j.id=target.job_id AND j.status='archived'
 JOIN routing_examples r ON r.id=(SELECT MAX(id) FROM routing_examples WHERE source_job_id=j.id)
 WHERE source.job_id=? AND source.model_key=?
 ), ranked AS (
 SELECT *, ROW_NUMBER() OVER (PARTITION BY job_id ORDER BY distance, excerpt, query_excerpt) AS rank FROM candidates
 ) SELECT job_id,filename,folder,recipient,recipient_scope,document_type,excerpt,query_excerpt,distance
 FROM ranked WHERE rank=1 ORDER BY distance,job_id LIMIT 5`, jobID, model)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	matches := []EmbeddingMatch{}
	for rows.Next() {
		var m EmbeddingMatch
		if err := rows.Scan(&m.JobID, &m.Filename, &m.Folder, &m.Recipient, &m.RecipientScope, &m.DocumentType, &m.Excerpt, &m.QueryExcerpt, &m.Distance); err != nil {
			return nil, err
		}
		matches = append(matches, m)
	}
	return matches, rows.Err()
}
