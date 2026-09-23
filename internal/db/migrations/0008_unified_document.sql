ALTER TABLE document_blocks ADD COLUMN unified_json TEXT NOT NULL DEFAULT 'null';
ALTER TABLE embedding_chunks ADD COLUMN source_block_ids TEXT NOT NULL DEFAULT '[]';
