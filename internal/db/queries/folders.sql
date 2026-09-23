-- name: UpsertFolder :exec
INSERT INTO folders (path, source, first_seen, last_seen)
VALUES (?, ?, ?, ?)
ON CONFLICT(path) DO UPDATE SET last_seen = excluded.last_seen;

-- name: DeleteUnapprovedConfigFolders :exec
DELETE FROM folders
WHERE source = 'config'
  AND approved_count = 0
  AND NOT EXISTS (
    SELECT 1 FROM routing_examples WHERE routing_examples.folder = folders.path
  );

-- name: DeleteStaleArchiveFolders :exec
DELETE FROM folders
WHERE source = 'archive'
  AND last_seen <> ?
  AND approved_count = 0
  AND NOT EXISTS (
    SELECT 1 FROM routing_examples WHERE routing_examples.folder = folders.path
  );

-- name: ListFolders :many
SELECT path
FROM folders
ORDER BY path;

-- name: ListApprovedFolders :many
SELECT path
FROM folders
WHERE approved_count > 0
ORDER BY path;

-- name: GetFolderApprovedCount :one
SELECT approved_count
FROM folders
WHERE path = ?;

-- name: IncrementFolderApproval :exec
UPDATE folders
SET approved_count = approved_count + 1, last_seen = ?
WHERE path = ?;

-- name: InsertRoutingExample :exec
INSERT INTO routing_examples (
	created_at, source_job_id, sender, recipient, recipient_scope, folder, filename, weight
) VALUES (
	?, ?, ?, ?, ?, ?, ?, ?
);

-- name: ApprovedExampleCount :one
SELECT COUNT(*)
FROM routing_examples
WHERE lower(sender) = lower(@sender)
	AND lower(recipient) = lower(@recipient)
	AND recipient_scope = @recipient_scope
	AND folder = @folder;

-- name: ListRoutingExamples :many
SELECT sender, recipient, recipient_scope, folder, MAX(filename) AS filename, COUNT(*) AS approvals
FROM routing_examples
GROUP BY sender, recipient, recipient_scope, folder
ORDER BY MAX(id) DESC
LIMIT 500;

-- name: CountRoutingExamples :one
SELECT COUNT(*) FROM routing_examples;
