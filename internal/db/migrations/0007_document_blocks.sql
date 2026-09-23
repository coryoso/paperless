CREATE TABLE document_blocks (
 job_id TEXT PRIMARY KEY REFERENCES jobs(id) ON DELETE CASCADE,
 source_hash TEXT NOT NULL,
 version INTEGER NOT NULL,
 distance_multiplier REAL NOT NULL,
 blocks_json TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
ALTER TABLE embedding_chunks ADD COLUMN block_id INTEGER;
