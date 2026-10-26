-- name: ActivateRepository :one
insert into repositories (id, installation_id, full_name, private, default_branch, allow_public, activated_by)
values ($1, $2, $3, $4, $5, $6, $7)
on conflict (id) do update
set full_name = excluded.full_name, private = excluded.private,
    default_branch = excluded.default_branch, allow_public = excluded.allow_public,
    config_stale = true
where repositories.installation_id = excluded.installation_id
returning *;

-- name: DeactivateRepository :execrows
delete from repositories where installation_id = $1 and id = $2;

-- name: GetRepository :one
select * from repositories where installation_id = $1 and id = $2;

-- name: ListRepositories :many
select * from repositories where installation_id = $1 order by lower(full_name);

-- name: UpdateRepositoryMetadata :exec
-- Keeps name, visibility and default branch current from webhooks.
update repositories
set full_name = $2, private = $3, default_branch = coalesce(nullif(sqlc.arg(default_branch)::text, ''), default_branch)
where id = $1;

-- name: MarkRepositoryConfigStale :execrows
-- Clearing config_checked_at puts the repository first in line for the
-- next refresh pass.
update repositories set config_stale = true, config_checked_at = null where id = $1;

-- name: ListStaleRepositoryConfigs :many
-- Repositories whose default-branch config should be read again: changed
-- by a push, or not read for a while.
select * from repositories
where config_checked_at is null
    or (config_stale and config_checked_at < sqlc.arg(retry_before)::timestamptz)
    or config_checked_at < sqlc.arg(checked_before)::timestamptz
order by config_checked_at nulls first
limit sqlc.arg(max_results);

-- name: SetRepositoryConfig :exec
update repositories
set config_sha = sqlc.narg(config_sha), config_error = sqlc.narg(config_error),
    config_checked_at = now(), config_stale = false
where id = sqlc.arg(id);

-- name: TouchRepositoryConfig :exec
update repositories set config_checked_at = now() where id = $1;
