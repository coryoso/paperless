CREATE TABLE jobs (
	id TEXT PRIMARY KEY,
	source_filename TEXT NOT NULL,
	raw_path TEXT NOT NULL DEFAULT '',
	current_path TEXT NOT NULL DEFAULT '',
	final_path TEXT NOT NULL DEFAULT '',
	text_path TEXT NOT NULL DEFAULT '',
	file_hash TEXT NOT NULL DEFAULT '',
	text_hash TEXT NOT NULL DEFAULT '',
	scan_timestamp TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	status TEXT NOT NULL,
	page_count INTEGER NOT NULL DEFAULT 0,
	summary TEXT NOT NULL DEFAULT '',
	classification_json TEXT NOT NULL DEFAULT '',
	confidence REAL NOT NULL DEFAULT 0,
	physical_original_action TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT '',
	duplicate_of TEXT NOT NULL DEFAULT '',
	manual_override INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX idx_jobs_status ON jobs(status);
CREATE INDEX idx_jobs_hash ON jobs(file_hash);
CREATE INDEX idx_jobs_updated ON jobs(updated_at);

CREATE TABLE events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	job_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	level TEXT NOT NULL,
	message TEXT NOT NULL,
	FOREIGN KEY(job_id) REFERENCES jobs(id)
);

CREATE INDEX idx_events_job ON events(job_id, created_at);

CREATE TABLE folders (
	path TEXT PRIMARY KEY,
	source TEXT NOT NULL,
	first_seen TEXT NOT NULL,
	last_seen TEXT NOT NULL,
	approved_count INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE routing_examples (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	created_at TEXT NOT NULL,
	source_job_id TEXT NOT NULL DEFAULT '',
	sender TEXT NOT NULL DEFAULT '',
	document_type TEXT NOT NULL DEFAULT '',
	folder TEXT NOT NULL,
	filename TEXT NOT NULL DEFAULT '',
	weight REAL NOT NULL DEFAULT 1.0,
	FOREIGN KEY(folder) REFERENCES folders(path)
);

CREATE INDEX idx_routing_sender_type ON routing_examples(sender, document_type);
CREATE INDEX idx_routing_folder ON routing_examples(folder);
