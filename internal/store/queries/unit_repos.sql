-- name: UpsertUnitRepo :exec
INSERT INTO unit_repos (unit_id, repo_id, checkout_path, base_sha, updated_at)
VALUES (@unit_id, @repo_id, @checkout_path, @base_sha, @now)
ON CONFLICT (unit_id, repo_id) DO UPDATE SET checkout_path = excluded.checkout_path, base_sha = excluded.base_sha, updated_at = excluded.updated_at;

-- name: ListUnitRepos :many
SELECT ur.*, r.full_name, r.default_branch, r.clone_url, r.clone_path
FROM unit_repos ur JOIN repos r ON r.id = ur.repo_id
WHERE ur.unit_id = @unit_id
ORDER BY r.full_name;

-- name: SetUnitRepoTarget :exec
UPDATE unit_repos SET is_target = @is_target, updated_at = @now WHERE unit_id = @unit_id AND repo_id = @repo_id;

-- name: SetUnitRepoBranch :exec
UPDATE unit_repos SET branch = @branch, updated_at = @now WHERE unit_id = @unit_id AND repo_id = @repo_id;

-- name: SetUnitRepoPublish :exec
UPDATE unit_repos
SET publish_state = @publish_state, head_sha = @head_sha, pr_number = @pr_number, pr_url = @pr_url, pr_state = @pr_state, updated_at = @now
WHERE unit_id = @unit_id AND repo_id = @repo_id;

-- name: SetUnitRepoPRStatus :exec
UPDATE unit_repos
SET pr_state = @pr_state, checks_state = @checks_state, head_sha = @head_sha, merge_sha = @merge_sha, merged_at = @merged_at, updated_at = @now
WHERE unit_id = @unit_id AND repo_id = @repo_id;

-- name: SetUnitRepoReviewed :exec
UPDATE unit_repos SET reviewed_sha = @reviewed_sha, updated_at = @now WHERE unit_id = @unit_id AND repo_id = @repo_id;

-- name: SetUnitRepoRelease :exec
UPDATE unit_repos SET release_state = @release_state, release_runs = @release_runs, updated_at = @now
WHERE unit_id = @unit_id AND repo_id = @repo_id;
