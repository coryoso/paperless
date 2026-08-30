-- name: CreateJob :exec
INSERT INTO jobs (
	id, source_filename, current_path, scan_timestamp, updated_at, status
) VALUES (
	?, ?, ?, ?, ?, ?
);

-- name: AddEvent :exec
INSERT INTO events (job_id, created_at, level, message)
VALUES (?, ?, ?, ?);

-- name: SetRawCopy :exec
UPDATE jobs
SET raw_path = ?, file_hash = ?, status = ?, updated_at = ?
WHERE id = ?;

-- name: SetCurrentStatus :exec
UPDATE jobs
SET current_path = ?, status = ?, updated_at = ?
WHERE id = ?;

-- name: SetOCRComplete :exec
UPDATE jobs
SET current_path = ?, text_path = ?, text_hash = ?, page_count = ?, input_kind = ?, text_source = ?, status = ?, updated_at = ?
WHERE id = ?;

-- name: SetClassified :exec
UPDATE jobs
SET classification_json = ?, confidence = ?, summary = ?, physical_original_action = ?, status = ?, updated_at = ?
WHERE id = ?;

-- name: SetArchived :exec
UPDATE jobs
SET current_path = ?, final_path = ?, status = ?, updated_at = ?
WHERE id = ?;

-- name: SetNeedsReview :exec
UPDATE jobs
SET current_path = ?, status = ?, updated_at = ?
WHERE id = ?;

-- name: SetDuplicate :exec
UPDATE jobs
SET current_path = ?, status = ?, duplicate_of = ?, physical_original_action = ?, updated_at = ?
WHERE id = ?;

-- name: SetFailed :exec
UPDATE jobs
SET status = ?, error = ?, physical_original_action = ?, updated_at = ?
WHERE id = ?;

-- name: SetRejected :exec
UPDATE jobs
SET current_path = ?, status = ?, physical_original_action = ?, manual_override = ?, updated_at = ?
WHERE id = ?;

-- name: SetManualArchived :exec
UPDATE jobs
SET current_path = ?, final_path = ?, status = ?, physical_original_action = ?, manual_override = ?, updated_at = ?
WHERE id = ?;

-- name: GetJob :one
SELECT *
FROM jobs
WHERE id = ?;

-- name: FindDuplicateByHash :one
SELECT *
FROM jobs
WHERE file_hash = ? AND id != ?
ORDER BY scan_timestamp ASC
LIMIT 1;

-- name: ListRecentJobs :many
SELECT *
FROM jobs
ORDER BY updated_at DESC
LIMIT ?;

-- name: ListAllJobs :many
SELECT *
FROM jobs
ORDER BY updated_at DESC;

-- name: ListReviewJobs :many
SELECT *
FROM jobs
WHERE status IN ('needs_review', 'failed', 'rejected')
ORDER BY updated_at DESC
LIMIT ?;
