-- Older cached embeddings are rebuilt by the worker to add document centroids.
ALTER TABLE document_embeddings ADD COLUMN centroid BLOB;
