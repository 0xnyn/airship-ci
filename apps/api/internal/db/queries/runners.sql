-- name: CreateRunner :one
-- Returns no row when a live runner for the triggering job already exists.
insert into runners (
    installation_id, profile_id, profile_label, data_plane_id, repository_id,
    repository_full_name, trigger_job_id, name
)
values ($1, $2, $3, $4, $5, $6, $7, $8)
on conflict (trigger_job_id) where state <> 'finished' do nothing
returning *;

-- name: CountFailedRunnersForJob :one
-- Failed attempts at serving one job, to cap retries.
select count(*) from runners
where trigger_job_id = $1 and finish_reason in ('launch_failed', 'lease_expired', 'lost');

-- name: JobHasLiveRunner :one
select exists(select 1 from runners where trigger_job_id = $1 and state <> 'finished');

-- name: GetRunner :one
select * from runners where id = $1;

-- name: GetRunnerByName :one
select * from runners where installation_id = $1 and name = $2;

-- name: CountActiveRunnersByProfile :many
select profile_id, count(*)::integer as active
from runners
where data_plane_id = $1 and state in ('assigned', 'running', 'busy')
group by profile_id;

-- name: ClaimPendingRunners :many
update runners
set state = 'assigned',
    assigned_at = now(),
    lease_expires_at = now() + make_interval(secs => sqlc.arg(lease_seconds)::float8)
where id in (
    select candidates.id from runners as candidates
    where candidates.profile_id = sqlc.arg(profile_id) and candidates.state = 'pending'
        and (candidates.not_before is null or candidates.not_before <= now())
    order by candidates.created_at
    limit sqlc.arg(max_runners)
    for update skip locked
)
returning *;

-- name: ReleaseRunner :exec
-- Puts an assigned runner back in the queue after a transient failure, not
-- to be claimed again for retry_after_seconds.
update runners
set state = 'pending', assigned_at = null, lease_expires_at = null,
    not_before = now() + make_interval(secs => sqlc.arg(retry_after_seconds)::float8)
where id = sqlc.arg(id) and state = 'assigned';

-- name: RequeueUnacknowledgedRunners :many
-- Assigned runners the agent never received: their sync response was lost.
-- Only runners assigned before the agent sent this request count, because
-- a response still in flight when the request was sent cannot be in it.
update runners
set state = 'pending', assigned_at = null, lease_expires_at = null
where data_plane_id = sqlc.arg(data_plane_id)
    and state = 'assigned'
    and assigned_at < sqlc.arg(assigned_before)::timestamptz
    and not (id = any(sqlc.arg(acknowledged)::uuid[]))
returning *;

-- name: SetRunnerGitHubID :exec
update runners set github_runner_id = $2 where id = $1;

-- name: FinishRunner :one
-- Moves a runner to finished if it is still in one of from_states. Runners
-- that never got an instance are marked gone at the same time.
update runners
set state = 'finished',
    finish_reason = sqlc.arg(reason)::text,
    error = coalesce(sqlc.narg(error), error),
    finished_at = now(),
    lease_expires_at = null,
    instance_gone_at = case when instance_id is null and state = 'pending' then now() else instance_gone_at end,
    deregistered_at = case
        when github_runner_id is null or sqlc.arg(deregistered)::boolean then coalesce(deregistered_at, now())
        else deregistered_at
    end
where id = sqlc.arg(id) and state = any(sqlc.arg(from_states)::text[])
    and (sqlc.narg(data_plane_id)::uuid is null or data_plane_id = sqlc.narg(data_plane_id)::uuid)
returning *;

-- name: CancelPendingRunnersForJob :many
update runners
set state = 'finished', finish_reason = 'cancelled', finished_at = now(),
    instance_gone_at = now(), deregistered_at = coalesce(deregistered_at, now())
where trigger_job_id = $1 and state = 'pending'
returning *;

-- name: MarkRunnerBusy :one
update runners
set state = 'busy', busy_at = coalesce(busy_at, now())
where id = $1 and state in ('assigned', 'running')
returning *;

-- name: ReviveRunners :many
-- A runner whose lease expired but whose instance the agent now reports did
-- launch, and may already be running a job, so it is revived rather than
-- terminated, as long as GitHub still has it registered. It returns the job
-- it was started for, so the retry runner queued for that job can be
-- cancelled and the profile stays within max_runners.
with revived as (
    select runners.id, runners.trigger_job_id as old_trigger_job_id
    from runners
    where runners.id = any(sqlc.arg(runner_ids)::uuid[])
        and runners.data_plane_id = sqlc.arg(data_plane_id)
        and runners.state = 'finished'
        and runners.finish_reason = 'lease_expired'
        and runners.deregistered_at is null
    for update
)
update runners
set state = 'running', finish_reason = null, finished_at = null,
    trigger_job_id = null, instance_gone_at = null
from revived
where runners.id = revived.id
returning runners.id, revived.old_trigger_job_id;

-- name: ReportInstances :many
-- Records the instances the agent sees; assigned runners become running.
update runners
set state = case when runners.state = 'assigned' then 'running' else runners.state end,
    lease_expires_at = null,
    running_at = coalesce(runners.running_at, now()),
    instance_id = coalesce(runners.instance_id, reported.instance_id),
    instance_gone_at = null
from (
    select unnest(sqlc.arg(runner_ids)::uuid[]) as runner_id, unnest(sqlc.arg(instance_ids)::text[]) as instance_id
) as reported
where runners.id = reported.runner_id and runners.data_plane_id = sqlc.arg(data_plane_id)
returning runners.*;

-- name: MarkInstancesGone :many
-- Runners whose instance the agent no longer reports. A grace period covers
-- cloud APIs that list new instances late. A runner that was waiting for a
-- job is finished as lost; a busy runner waits for GitHub's completed event.
update runners
set instance_gone_at = now(),
    state = case when state = 'running' then 'finished' else state end,
    finish_reason = case when state = 'running' then 'lost' else finish_reason end,
    finished_at = case when state = 'running' then now() else finished_at end
where data_plane_id = sqlc.arg(data_plane_id)
    and instance_gone_at is null
    and state in ('running', 'busy', 'finished')
    and not (id = any(sqlc.arg(reported_ids)::uuid[]))
    and coalesce(running_at, finished_at, created_at) < sqlc.arg(seen_before)::timestamptz
returning *;

-- name: ExpireRunnerLeases :many
update runners
set state = 'finished', finish_reason = 'lease_expired', finished_at = now(), lease_expires_at = null
where state = 'assigned' and lease_expires_at < now()
returning *;

-- name: ListIdleRunners :many
select * from runners
where state = 'running' and running_at < sqlc.arg(running_before)::timestamptz
order by running_at
limit sqlc.arg(max_results);

-- name: ListLostBusyRunners :many
select * from runners
where state = 'busy'
    and (instance_gone_at < sqlc.arg(gone_before)::timestamptz or busy_at < sqlc.arg(busy_before)::timestamptz)
limit sqlc.arg(max_results);

-- name: ListRunnersToDeregister :many
select * from runners
where state = 'finished' and github_runner_id is not null and deregistered_at is null
    and finished_at < sqlc.arg(finished_before)::timestamptz
order by finished_at
limit sqlc.arg(max_results);

-- name: MarkRunnerDeregistered :exec
update runners set deregistered_at = now() where id = $1;

-- name: ListRunners :many
select * from runners
where runners.installation_id = sqlc.arg(installation_id)
    and (sqlc.arg(active_only)::boolean = false or runners.state <> 'finished')
    and (sqlc.narg(cursor_created_at)::timestamptz is null
        or (runners.created_at, runners.id) < (sqlc.narg(cursor_created_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
order by runners.created_at desc, runners.id desc
limit sqlc.arg(max_results);

-- name: CountActiveRunners :many
select state, count(*)::bigint as runners
from runners
where installation_id = $1 and state <> 'finished'
group by state;

-- name: FinishInstallationRunners :many
-- The app was uninstalled: nothing more will run, so every runner ends and
-- agents terminate the instances on their next sync.
update runners
set state = 'finished', finish_reason = 'deleted', finished_at = now(), lease_expires_at = null,
    instance_gone_at = case when instance_id is null then now() else instance_gone_at end,
    deregistered_at = coalesce(deregistered_at, now())
where installation_id = $1 and state <> 'finished'
returning *;

-- name: ClearRunnerTrigger :exec
update runners set trigger_job_id = null where id = $1;

-- name: MoveLiveRunners :execrows
-- Hands a job's spare runner to another job that lost its own.
update runners set trigger_job_id = sqlc.arg(to_job_id)
where trigger_job_id = sqlc.arg(from_job_id) and state <> 'finished';

-- name: CountActiveRunnersForProfile :one
select count(*) from runners where profile_id = $1 and state <> 'finished';
