-- name: CreateEvent :exec
insert into events (installation_id, kind, level, message, repository, runner_id, job_id)
values ($1, $2, $3, $4, $5, $6, $7);

-- name: ListEvents :many
select * from events
where installation_id = sqlc.arg(installation_id)
    and (sqlc.narg(cursor_created_at)::timestamptz is null
        or (created_at, id) < (sqlc.narg(cursor_created_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
    and (sqlc.narg(level)::text is null or level = sqlc.narg(level)::text)
order by created_at desc, id desc
limit sqlc.arg(max_results);

-- name: DeleteOldEvents :execrows
delete from events where created_at < sqlc.arg(before)::timestamptz;
