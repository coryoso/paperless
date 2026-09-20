-- A rebuildable, model-versioned index. Original documents remain authoritative.
CREATE TABLE document_embeddings (
 job_id TEXT PRIMARY KEY REFERENCES jobs(id) ON DELETE CASCADE,
 content_hash TEXT NOT NULL,
 model_key TEXT NOT NULL,
 dimensions INTEGER NOT NULL,
 indexed_at TEXT NOT NULL
);
CREATE TABLE embedding_chunks (
 job_id TEXT NOT NULL REFERENCES document_embeddings(job_id) ON DELETE CASCADE,
 ordinal INTEGER NOT NULL,
 excerpt TEXT NOT NULL,
 vector BLOB NOT NULL,
 PRIMARY KEY(job_id, ordinal)
);
CREATE INDEX idx_document_embeddings_model ON document_embeddings(model_key, dimensions);

CREATE INDEX idx_routing_source_job ON routing_examples(source_job_id, id);
