-- name: CreateDocument :one
INSERT INTO documents (id, unit_id, kind, version, content, meta, author, run_id, created_at)
VALUES (@id, @unit_id, @kind,
        (SELECT CAST(COALESCE(MAX(d.version), 0) + 1 AS INTEGER) FROM documents d WHERE d.unit_id = @unit_id AND d.kind = @kind),
        @content, @meta, @author, @run_id, @now)
RETURNING *;

-- name: LatestDocument :one
SELECT * FROM documents WHERE unit_id = @unit_id AND kind = @kind ORDER BY version DESC LIMIT 1;

-- name: GetDocumentVersion :one
SELECT * FROM documents WHERE unit_id = @unit_id AND kind = @kind AND version = @version;

-- name: ListDocumentVersions :many
SELECT id, kind, version, author, run_id, created_at FROM documents
WHERE unit_id = @unit_id AND kind = @kind ORDER BY version DESC;
