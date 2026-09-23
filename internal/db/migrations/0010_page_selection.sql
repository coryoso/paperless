CREATE TABLE document_pages (
 job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
 page INTEGER NOT NULL CHECK(page > 0),
 suggested_blank INTEGER NOT NULL,
 excluded INTEGER NOT NULL,
 reason TEXT NOT NULL,
 PRIMARY KEY(job_id,page)
);
