-- name: CreateArtifact :one
INSERT INTO artifacts (id, unit_id, path, version, content, content_type, size, sha256, removed, author, run_id, created_at)
VALUES (@id, @unit_id, @path,
        (SELECT CAST(COALESCE(MAX(a.version), 0) + 1 AS INTEGER) FROM artifacts a WHERE a.unit_id = @unit_id AND a.path = @path),
        @content, @content_type, @size, @sha256, @removed, @author, @run_id, @now)
RETURNING id, unit_id, path, version, content_type, size, sha256, removed, author, run_id, created_at;

-- name: LatestArtifacts :many
-- The latest version of each of a unit's artifacts, removed ones included.
SELECT a.id, a.unit_id, a.path, a.version, a.content_type, a.size, a.sha256, a.removed, a.author, a.run_id, a.created_at
FROM artifacts a
WHERE a.unit_id = @unit_id
  AND a.version = (SELECT MAX(b.version) FROM artifacts b WHERE b.unit_id = a.unit_id AND b.path = a.path)
ORDER BY a.path;

-- name: GetArtifactVersion :one
SELECT * FROM artifacts WHERE unit_id = @unit_id AND path = @path AND version = @version;

-- name: ListArtifactVersions :many
SELECT id, unit_id, path, version, content_type, size, sha256, removed, author, run_id, created_at FROM artifacts
WHERE unit_id = @unit_id AND path = @path ORDER BY version DESC;
