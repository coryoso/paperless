ALTER TABLE document_pages ADD COLUMN position INTEGER CHECK(position > 0);
