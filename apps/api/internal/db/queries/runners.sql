-- name: CreateRunner :one
-- Returns no row when a live runner for the triggering job already exists.
insert into runners (
    installation_id, definition, source, scope, cloud, credential_id, region, spec, labels, attempt,
    pool_id, repository_id, repository_full_name, trigger_job_id, name
)
values (
    sqlc.arg(installation_id), sqlc.arg(definition), sqlc.arg(source), sqlc.arg(scope), sqlc.arg(cloud),
    sqlc.narg(credential_id), sqlc.arg(region), sqlc.arg(spec), sqlc.arg(labels)::text[],
    sqlc.arg(attempt), sqlc.narg(pool_id), sqlc.arg(repository_id), sqlc.arg(repository_full_name),
    sqlc.narg(trigger_job_id), sqlc.arg(name)
)
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

-- name: GetInstallationRunner :one
select * from runners where installation_id = $1 and id = $2;

-- name: ListInstallationsWithPendingRunners :many
-- Installations that have work for the launcher, excluding suspended and
-- uninstalled ones, whose runners wait until they are unsuspended.
select distinct runners.installation_id
from runners
join installations on installations.id = runners.installation_id
where runners.state = 'pending'
    and (runners.not_before is null or runners.not_before <= now())
    and installations.deleted_at is null
    and installations.suspended_at is null;

-- name: LockInstallation :one
-- Serializes claiming within one installation, so its max_runners holds
-- across concurrent launchers on several API replicas.
select max_runners from installations where id = $1 for update;

-- name: CountActiveRunners :one
select count(*)::integer from runners
where installation_id = $1 and state in ('launching', 'running', 'busy');

-- name: ClaimRunners :many
-- Moves up to max_results pending runners of one installation to
-- launching, oldest first, with a lease. The caller holds the installation
-- lock and passes how many its cap allows.
update runners
set state = 'launching',
    launching_at = now(),
    lease_expires_at = now() + make_interval(secs => sqlc.arg(lease_seconds)::float8)
where id in (
    select candidates.id from runners as candidates
    where candidates.installation_id = sqlc.arg(installation_id) and candidates.state = 'pending'
        and (candidates.not_before is null or candidates.not_before <= now())
    order by candidates.created_at
    limit sqlc.arg(max_results)
    for update skip locked
)
returning *;

-- name: ReleaseRunner :exec
-- Puts a launching runner back in the queue after a transient failure, not
-- to be claimed again for retry_after_seconds.
update runners
set state = 'pending', launching_at = null, lease_expires_at = null,
    not_before = now() + make_interval(secs => sqlc.arg(retry_after_seconds)::float8)
where id = sqlc.arg(id) and state = 'launching';

-- name: SetRunnerGitHubID :exec
update runners set github_runner_id = $2 where id = $1;

-- name: MarkRunnerRunning :one
-- The cloud accepted the launch.
update runners
set state = 'running', running_at = now(), lease_expires_at = null,
    instance_id = sqlc.arg(instance_id), instance_seen_at = now()
where id = sqlc.arg(id) and state = 'launching'
returning *;

-- name: FinishRunner :one
-- Moves a runner to finished if it is still in one of from_states. Runners
-- that never got an instance are marked gone at the same time.
update runners
set state = 'finished',
    finish_reason = sqlc.arg(reason)::text,
    error = coalesce(sqlc.narg(error), error),
    finished_at = now(),
    lease_expires_at = null,
    instance_gone_at = case when instance_id is null then now() else instance_gone_at end,
    deregistered_at = case
        when github_runner_id is null or sqlc.arg(deregistered)::boolean then coalesce(deregistered_at, now())
        else deregistered_at
    end
where id = sqlc.arg(id) and state = any(sqlc.arg(from_states)::text[])
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
where id = $1 and state in ('launching', 'running')
returning *;

-- name: ListInventoryScopes :many
-- Every (credential, region) with runners whose instance may still exist,
-- so the reconciler knows where to look.
select distinct credential_id, region
from runners
where credential_id is not null and instance_gone_at is null and state <> 'pending';

-- name: ReviveRunners :many
-- A runner whose lease expired but whose instance the cloud now lists did
-- launch, and may already be running a job, so it is revived rather than
-- terminated, as long as GitHub still has it registered. It returns the
-- job it was started for, so the retry runner queued for that job can be
-- cancelled and the installation stays within max_runners.
with revived as (
    select runners.id, runners.trigger_job_id as old_trigger_job_id
    from runners
    where runners.id = any(sqlc.arg(runner_ids)::uuid[])
        and runners.credential_id = sqlc.arg(credential_id)
        and runners.state = 'finished'
        and runners.finish_reason = 'lease_expired'
        and runners.deregistered_at is null
    for update
)
update runners
set state = 'running', finish_reason = null, finished_at = null,
    trigger_job_id = null, instance_gone_at = null, running_at = coalesce(running_at, now())
from revived
where runners.id = revived.id
returning runners.id, revived.old_trigger_job_id;

-- name: ReportInstances :many
-- Records the instances the cloud lists for a (credential, region).
update runners
set instance_seen_at = now(),
    instance_id = coalesce(runners.instance_id, reported.instance_id),
    instance_gone_at = null
from (
    select unnest(sqlc.arg(runner_ids)::uuid[]) as runner_id, unnest(sqlc.arg(instance_ids)::text[]) as instance_id
) as reported
where runners.id = reported.runner_id and runners.credential_id = sqlc.arg(credential_id)
    and runners.region = sqlc.arg(region)
returning runners.*;

-- name: MarkInstancesGone :many
-- Runners in a (credential, region) whose instance the cloud no longer
-- lists. A grace period covers clouds that list new instances late. A
-- runner that was waiting for a job is finished as lost; a busy runner
-- waits for GitHub's completed event.
update runners
set instance_gone_at = now(),
    state = case when state = 'running' then 'finished' else state end,
    finish_reason = case when state = 'running' then 'lost' else finish_reason end,
    finished_at = case when state = 'running' then now() else finished_at end
where credential_id = sqlc.arg(credential_id)
    and region = sqlc.arg(region)
    and instance_gone_at is null
    and state in ('running', 'busy', 'finished')
    and not (id = any(sqlc.arg(reported_ids)::uuid[]))
    and coalesce(running_at, finished_at, created_at) < sqlc.arg(seen_before)::timestamptz
returning *;

-- name: ExpireRunnerLeases :many
-- The instance may exist (the launcher died after the cloud call), so
-- instance_gone_at is left for the inventory scan to decide: it revives
-- the runner if the instance turns up, or marks it gone after the grace
-- period.
update runners
set state = 'finished', finish_reason = 'lease_expired', finished_at = now(), lease_expires_at = null
where state = 'launching' and lease_expires_at < now()
returning *;

-- name: ListIdleRunners :many
-- Launched runners that never got a job. Warm-pool runners waiting for one
-- are idle on purpose; their pool decides when they go.
select * from runners
where state = 'running' and running_at < sqlc.arg(running_before)::timestamptz
    and (pool_id is null or trigger_job_id is not null)
order by running_at
limit sqlc.arg(max_results);

-- name: ListLostBusyRunners :many
select * from runners
where state = 'busy'
    and (instance_gone_at < sqlc.arg(gone_before)::timestamptz or busy_at < sqlc.arg(busy_before)::timestamptz)
limit sqlc.arg(max_results);

-- name: ListRunnersToTerminate :many
-- Finished runners whose instance may still be running: never asked to
-- terminate, or asked a while ago and still listed.
select * from runners
where state = 'finished' and instance_id is not null and instance_gone_at is null
    and (terminate_requested_at is null or terminate_requested_at < sqlc.arg(requested_before)::timestamptz)
order by finished_at
limit sqlc.arg(max_results);

-- name: MarkTerminateRequested :exec
update runners set terminate_requested_at = now() where id = $1;

-- name: ListRunnersToDeregister :many
select * from runners
where state = 'finished' and github_runner_id is not null and deregistered_at is null
    and finished_at < sqlc.arg(finished_before)::timestamptz
order by finished_at
limit sqlc.arg(max_results);

-- name: MarkRunnerDeregistered :exec
update runners set deregistered_at = now() where id = $1;

-- name: SetRunnerProvisionSecret :exec
update runners set provision_secret = $2 where id = $1;

-- name: MarkRunnerProvisioned :one
-- Hands out the boot script once per runner, while it is still booting,
-- and forgets it. The sealed script comes back as boot_secret.
with target as (
    select candidates.id, candidates.provision_secret from runners as candidates
    where candidates.installation_id = sqlc.arg(installation_id) and candidates.name = sqlc.arg(name)
        and candidates.state in ('launching', 'running') and candidates.provisioned_at is null
        and candidates.provision_secret is not null
    for update
)
update runners
set provisioned_at = now(), provision_secret = null
from target
where runners.id = target.id
returning runners.id, runners.installation_id, target.provision_secret::bytea as boot_secret;

-- name: ListRunners :many
select * from runners
where runners.installation_id = sqlc.arg(installation_id)
    and (sqlc.arg(active_only)::boolean = false or runners.state <> 'finished')
    and (sqlc.narg(cursor_created_at)::timestamptz is null
        or (runners.created_at, runners.id) < (sqlc.narg(cursor_created_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
order by runners.created_at desc, runners.id desc
limit sqlc.arg(max_results);

-- name: CountRunnersByState :many
select state, count(*)::bigint as runners
from runners
where installation_id = $1 and state <> 'finished'
group by state;

-- name: CountActiveRunnersForCredential :one
select count(*) from runners where credential_id = $1 and state <> 'finished';

-- name: FinishInstallationRunners :many
-- The app was uninstalled: nothing more will run, so every runner ends and
-- the reconciler terminates the instances.
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

-- name: SetRunnerCache :exec
update runners set cache_token_hash = $2, cache_bucket = $3 where id = $1;

-- name: AuthenticateRunnerCacheToken :one
-- A live runner's cache proxy, by its token. Finished runners and
-- uninstalled or suspended installations get nothing.
select sqlc.embed(runners), sqlc.embed(installations)
from runners
join installations on installations.id = runners.installation_id
where runners.cache_token_hash = $1
    and runners.state in ('launching', 'running', 'busy')
    and installations.deleted_at is null
    and installations.suspended_at is null;
