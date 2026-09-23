package db

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"sort"
)

type EmbeddingMatch struct {
	JobID          string  `json:"job_id"`
	Filename       string  `json:"filename"`
	Folder         string  `json:"folder"`
	Recipient      string  `json:"recipient"`
	RecipientScope string  `json:"recipient_scope"`
	Excerpt        string  `json:"excerpt"`
	QueryExcerpt   string  `json:"query_excerpt"`
	Distance       float64 `json:"-"`
}

func (s *Store) EmbeddingCurrent(ctx context.Context, jobID, hash, model string) (bool, error) {
	var count int
	err := s.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM document_embeddings WHERE job_id=? AND content_hash=? AND model_key=? AND centroid IS NOT NULL`, jobID, hash, model).Scan(&count)
	return count == 1, err
}

func (s *Store) DeleteEmbedding(ctx context.Context, jobID string) error {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM embedding_chunks WHERE job_id=?`, jobID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM document_embeddings WHERE job_id=?`, jobID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) SaveEmbedding(ctx context.Context, jobID, hash, model string, chunks []string, vectors [][]float32, blockIDs ...[]int) error {
	var sources [][]int
	if len(blockIDs) > 0 {
		for _, id := range blockIDs[0] {
			sources = append(sources, []int{id})
		}
	}
	return s.SaveDocumentEmbedding(ctx, jobID, hash, model, chunks, vectors, sources)
}
func (s *Store) SaveDocumentEmbedding(ctx context.Context, jobID, hash, model string, chunks []string, vectors [][]float32, sources [][]int) error {
	if len(chunks) == 0 || len(chunks) != len(vectors) || len(vectors[0]) == 0 || len(vectors[0]) > 4096 {
		return errors.New("invalid document embeddings")
	}
	if sources != nil && len(sources) != len(chunks) {
		return errors.New("invalid embedding block references")
	}
	dimension := len(vectors[0])
	normalized := make([][]float32, len(vectors))
	centroid := make([]float32, dimension)
	for i, vector := range vectors {
		if len(vector) != dimension {
			return errors.New("inconsistent document embeddings")
		}
		var err error
		normalized[i], err = normalizeVector(vector)
		if err != nil {
			return err
		}
		for n, v := range normalized[i] {
			centroid[n] += v / float32(len(vectors))
		}
	}
	centroid, err := normalizeVector(centroid)
	if err != nil {
		// Opposing sections can cancel exactly; retain a useful representative.
		centroid = normalized[0]
	}
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM embedding_chunks WHERE job_id=?`, jobID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM document_embeddings WHERE job_id=?`, jobID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO document_embeddings(job_id,content_hash,model_key,dimensions,indexed_at,centroid) VALUES(?,?,?,?,?,?)`, jobID, hash, model, dimension, Now(), encodeVector(centroid)); err != nil {
		return err
	}
	for i, vector := range normalized {
		data := encodeVector(vector)
		var blockID any
		sourceJSON := "[]"
		if sources != nil && len(sources[i]) > 0 {
			blockID = sources[i][0]
			raw, _ := json.Marshal(sources[i])
			sourceJSON = string(raw)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO embedding_chunks(job_id,ordinal,excerpt,vector,block_id,source_block_ids) VALUES(?,?,?,?,?,?)`, jobID, i, chunks[i], data, blockID, sourceJSON); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Shortlist by document centroid before comparing passages. The expensive
// section comparisons happen in Go after closing rows, releasing SQLite's sole
// connection for approvals and other requests. Models and dimensions stay isolated.
const similarityCandidateLimit = 32

func (s *Store) SimilarDocuments(ctx context.Context, jobID, model string) ([]EmbeddingMatch, error) {
	sourceRows, err := s.conn.QueryContext(ctx, `SELECT c.excerpt,c.vector
 FROM embedding_chunks c JOIN document_embeddings d ON d.job_id=c.job_id
 WHERE d.job_id=? AND d.model_key=? AND d.centroid IS NOT NULL ORDER BY c.ordinal`, jobID, model)
	if err != nil {
		return nil, err
	}
	source, err := readSourceSections(sourceRows)
	if err != nil {
		return nil, err
	}
	matches := []EmbeddingMatch{}
	if len(source) == 0 {
		return matches, nil
	}

	rows, err := s.conn.QueryContext(ctx, `WITH candidates AS MATERIALIZED (
 SELECT target.job_id
 FROM document_embeddings source
 JOIN document_embeddings target ON target.model_key=source.model_key AND target.dimensions=source.dimensions AND target.job_id<>source.job_id
 JOIN jobs j ON j.id=target.job_id AND j.status='archived'
 WHERE source.job_id=? AND source.model_key=? AND target.centroid IS NOT NULL
 AND EXISTS (SELECT 1 FROM routing_examples WHERE source_job_id=target.job_id)
 ORDER BY vec_distance_cosine(source.centroid,target.centroid),target.job_id LIMIT ?
 ) SELECT c.job_id,r.filename,r.folder,r.recipient,r.recipient_scope,c.excerpt,c.vector
 FROM candidates candidate
 JOIN embedding_chunks c ON c.job_id=candidate.job_id
 JOIN routing_examples r ON r.id=(SELECT MAX(id) FROM routing_examples WHERE source_job_id=c.job_id)
 ORDER BY c.job_id,c.ordinal`, jobID, model, similarityCandidateLimit)
	if err != nil {
		return nil, err
	}
	candidates, err := readCandidateSections(rows)
	if err != nil {
		return nil, err
	}

	best := map[string]EmbeddingMatch{}
	for _, candidate := range candidates {
		for _, query := range source {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if len(candidate.vector) != len(query.vector) {
				return nil, errors.New("inconsistent cached embedding dimensions")
			}
			var dot float64
			for i, v := range query.vector {
				dot += float64(v) * float64(candidate.vector[i])
			}
			match := candidate.match
			match.Distance = 1 - max(-1, min(1, dot))
			match.QueryExcerpt = query.excerpt
			previous, ok := best[match.JobID]
			if !ok || match.Distance < previous.Distance ||
				(match.Distance == previous.Distance && (match.Excerpt < previous.Excerpt ||
					(match.Excerpt == previous.Excerpt && match.QueryExcerpt < previous.QueryExcerpt))) {
				best[match.JobID] = match
			}
		}
	}
	for _, match := range best {
		matches = append(matches, match)
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Distance == matches[j].Distance {
			return matches[i].JobID < matches[j].JobID
		}
		return matches[i].Distance < matches[j].Distance
	})
	return matches[:min(5, len(matches))], nil
}

type sourceSection struct {
	excerpt string
	vector  []float32
}
type candidateSection struct {
	match  EmbeddingMatch
	vector []float32
}

func readSourceSections(rows *sql.Rows) ([]sourceSection, error) {
	defer rows.Close()
	var sections []sourceSection
	for rows.Next() {
		var section sourceSection
		var data []byte
		if err := rows.Scan(&section.excerpt, &data); err != nil {
			return nil, err
		}
		var err error
		section.vector, err = decodeVector(data)
		if err != nil {
			return nil, err
		}
		sections = append(sections, section)
	}
	return sections, rows.Err()
}

func readCandidateSections(rows *sql.Rows) ([]candidateSection, error) {
	defer rows.Close()
	var sections []candidateSection
	for rows.Next() {
		var section candidateSection
		var data []byte
		m := &section.match
		if err := rows.Scan(&m.JobID, &m.Filename, &m.Folder, &m.Recipient, &m.RecipientScope, &m.Excerpt, &data); err != nil {
			return nil, err
		}
		var err error
		section.vector, err = decodeVector(data)
		if err != nil {
			return nil, err
		}
		sections = append(sections, section)
	}
	return sections, rows.Err()
}

func normalizeVector(vector []float32) ([]float32, error) {
	var norm float64
	for _, v := range vector {
		norm += float64(v) * float64(v)
	}
	if len(vector) == 0 || len(vector) > 4096 || norm == 0 || math.IsNaN(norm) || math.IsInf(norm, 0) {
		return nil, errors.New("invalid vector")
	}
	result := make([]float32, len(vector))
	norm = math.Sqrt(norm)
	for i, v := range vector {
		result[i] = float32(float64(v) / norm)
	}
	return result, nil
}

func encodeVector(vector []float32) []byte {
	data := make([]byte, len(vector)*4)
	for i, v := range vector {
		binary.LittleEndian.PutUint32(data[i*4:], math.Float32bits(v))
	}
	return data
}

func decodeVector(data []byte) ([]float32, error) {
	if len(data)%4 != 0 {
		return nil, errors.New("invalid vector encoding")
	}
	vector := make([]float32, len(data)/4)
	for i := range vector {
		vector[i] = math.Float32frombits(binary.LittleEndian.Uint32(data[i*4:]))
	}
	return normalizeVector(vector)
}
