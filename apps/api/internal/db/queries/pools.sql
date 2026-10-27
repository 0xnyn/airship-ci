-- name: UpsertIdlePool :one
insert into idle_pools (installation_id, repository_id, name, plan, idle, source_sha)
values ($1, $2, $3, $4, $5, $6)
on conflict (installation_id, coalesce(repository_id, 0), name) do update
set plan = excluded.plan, idle = excluded.idle, source_sha = excluded.source_sha, updated_at = now()
returning *;

-- name: DeleteIdlePoolsExcept :execrows
-- Removes pools a config no longer defines. Their runners lose pool_id and
-- the idle reaper takes them.
delete from idle_pools
where repository_id = sqlc.arg(repository_id) and not (name = any(sqlc.arg(keep)::text[]));

-- name: DeleteOrganizationIdlePoolsExcept :execrows
delete from idle_pools
where installation_id = sqlc.arg(installation_id) and repository_id is null and not (name = any(sqlc.arg(keep)::text[]));

-- name: ListOrganizationIdlePools :many
select * from idle_pools where installation_id = $1 and repository_id is null order by name;

-- name: SetIdlePoolRunnerGroup :exec
update idle_pools set runner_group_id = $2 where id = $1;

-- name: ExtendOrganizationUsagePools :execrows
update idle_pools
set usage_until = now() + make_interval(mins => (idle->>'usageMinutes')::int)
where installation_id = sqlc.arg(installation_id) and repository_id is null
    and name = sqlc.arg(name)
    and (idle->>'usageMinutes') is not null;

-- name: ListIdlePools :many
select * from idle_pools order by installation_id, id;

-- name: ListRepositoryIdlePools :many
select * from idle_pools where repository_id = $1 order by name;

-- name: ListInstallationIdlePools :many
select * from idle_pools where installation_id = $1 order by repository_id, name;

-- name: LockIdlePool :one
-- Serializes filling and trimming one pool, so two passes cannot both see
-- a deficit and overshoot it.
select * from idle_pools where id = $1 for update;

-- name: ExtendUsagePools :execrows
-- A runner from this definition was allocated a job: usage-based pools for
-- it stay warm for their duration from now.
update idle_pools
set usage_until = now() + make_interval(mins => (idle->>'usageMinutes')::int)
where repository_id = sqlc.arg(repository_id)
    and name = sqlc.arg(name)
    and (idle->>'usageMinutes') is not null;

-- name: CountIdlePoolRunners :one
-- Runners in the pool still waiting for a job, launched or not.
select count(*)::integer from runners
where pool_id = $1 and trigger_job_id is null and state in ('pending', 'launching', 'running');

-- name: CountRecentPoolFailures :one
select count(*) from runners
where pool_id = $1 and finish_reason in ('launch_failed', 'launch_unconfirmed', 'lease_expired', 'lost')
    and finished_at > sqlc.arg(since)::timestamptz;

-- name: ListSurplusPoolRunners :many
-- Idle pool runners to trim, pending ones first since they cost nothing to
-- drop. Assigned runners are left alone: their agent may be booting them,
-- and they are trimmed once running.
select * from runners
where pool_id = sqlc.arg(pool_id) and trigger_job_id is null and state in ('pending', 'running')
order by state = 'pending' desc, created_at desc
limit sqlc.arg(max_results);

-- name: ReserveIdleRunner :one
-- Points an idle pool runner at a queued job, so the job is not given an
-- on-demand runner too and the pool replaces the one it will take.
update runners
set trigger_job_id = sqlc.arg(job_id), reserved_at = now()
where id = (
    select candidates.id from runners as candidates
    where candidates.pool_id = sqlc.arg(pool_id)
        and candidates.trigger_job_id is null
        and candidates.state in ('pending', 'launching', 'running')
    order by candidates.state = 'running' desc, candidates.created_at
    limit 1
    for update skip locked
)
returning *;

-- name: ReturnPoolRunners :execrows
-- GitHub gave the job a different runner, so pool runners reserved for it
-- go back to waiting in their pool.
update runners set trigger_job_id = null, reserved_at = null
where trigger_job_id = sqlc.arg(job_id) and pool_id is not null
    and state in ('pending', 'launching', 'running')
    and id <> sqlc.arg(except_id);

-- name: DetachPoolRunner :one
-- Takes an idle runner out of its pool before it is trimmed.
update runners set pool_id = null
where id = $1 and pool_id is not null and trigger_job_id is null and state = 'running'
returning *;

-- name: CountInstallationPoolRunners :many
-- Per pool: runners waiting for a job and runners running one.
select pool_id,
    count(*) filter (where trigger_job_id is null and state in ('pending', 'launching', 'running'))::integer as idle,
    count(*) filter (where state = 'busy' or (trigger_job_id is not null and state <> 'finished'))::integer as taken
from runners
where installation_id = $1 and pool_id is not null and state <> 'finished'
group by pool_id;

-- name: GetIdlePool :one
select * from idle_pools where id = $1;
