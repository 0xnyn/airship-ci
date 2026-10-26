-- name: UpsertJob :one
-- Folds one workflow_job event into the job row. Status only moves forward
-- and fields GitHub fills in later are never cleared by an older event.
insert into jobs (
    id, installation_id, repository_id, repository_full_name, run_id,
    run_attempt, workflow_name, name, head_branch, head_sha, html_url, labels, status,
    conclusion, plan, skip_reason, skip_detail, runner_name, queued_at, started_at, completed_at
)
values (
    sqlc.arg(id), sqlc.arg(installation_id), sqlc.arg(repository_id),
    sqlc.arg(repository_full_name), sqlc.arg(run_id), sqlc.arg(run_attempt),
    sqlc.narg(workflow_name), sqlc.arg(name), sqlc.narg(head_branch), sqlc.narg(head_sha),
    sqlc.narg(html_url), sqlc.arg(labels)::text[], sqlc.arg(status),
    sqlc.narg(conclusion), sqlc.narg(plan), sqlc.narg(skip_reason),
    sqlc.narg(skip_detail), sqlc.narg(runner_name),
    sqlc.arg(queued_at), sqlc.narg(started_at), sqlc.narg(completed_at)
)
on conflict (id) do update
set
    status = case
        when job_status_rank(excluded.status) > job_status_rank(jobs.status) then excluded.status
        else jobs.status
    end,
    conclusion = coalesce(excluded.conclusion, jobs.conclusion),
    runner_name = coalesce(excluded.runner_name, jobs.runner_name),
    started_at = coalesce(jobs.started_at, excluded.started_at),
    completed_at = coalesce(jobs.completed_at, excluded.completed_at),
    workflow_name = coalesce(excluded.workflow_name, jobs.workflow_name),
    head_branch = coalesce(excluded.head_branch, jobs.head_branch),
    html_url = coalesce(excluded.html_url, jobs.html_url),
    head_sha = coalesce(jobs.head_sha, excluded.head_sha),
    -- A job is planned once, when first seen queued; later events reuse it.
    plan = coalesce(jobs.plan, excluded.plan),
    -- A job skipped earlier and planned now (its repository was activated
    -- since, say) loses the old reason.
    skip_reason = case
        when jobs.plan is not null then jobs.skip_reason
        when excluded.plan is not null then null
        else coalesce(excluded.skip_reason, jobs.skip_reason)
    end,
    skip_detail = case
        when jobs.plan is not null then jobs.skip_detail
        when excluded.plan is not null then null
        else coalesce(excluded.skip_detail, jobs.skip_detail)
    end,
    updated_at = now()
returning *;

-- name: SetJobRunner :exec
update jobs set runner_id = sqlc.arg(runner_id), updated_at = now()
where id = sqlc.arg(id);

-- name: GetJob :one
select * from jobs where id = $1;

-- name: ListStaleQueuedJobs :many
-- Queued jobs Airship should run that it has heard nothing about for a
-- while; the reconciler asks GitHub about them in case a webhook was lost.
-- Each installation gets at most per_installation jobs per pass, so one
-- backlog cannot use up its own GitHub rate limit or delay other
-- installations. Jobs with no live runner come first: a lost webhook hurts
-- them most, while a job waiting for capacity only risks a wasted launch.
select sqlc.embed(jobs) from (
    select jobs.id,
        row_number() over (
            partition by jobs.installation_id
            order by exists (
                select 1 from runners
                where runners.trigger_job_id = jobs.id and runners.state <> 'finished'
            ), jobs.updated_at
        ) as rank
    from jobs
    where jobs.status = 'queued' and jobs.plan is not null
        and jobs.updated_at < sqlc.arg(before)
) as stale
join jobs on jobs.id = stale.id
where stale.rank <= sqlc.arg(per_installation)::bigint
order by stale.rank, jobs.updated_at
limit sqlc.arg(max_results);

-- name: TouchJob :exec
update jobs set updated_at = now() where id = $1;

-- name: ListJobs :many
select * from jobs
where installation_id = sqlc.arg(installation_id)
    and (sqlc.narg(cursor_queued_at)::timestamptz is null
        or (queued_at, id) < (sqlc.narg(cursor_queued_at)::timestamptz, sqlc.narg(cursor_id)::bigint))
    and (sqlc.narg(status)::text is null or status = sqlc.narg(status)::text)
order by queued_at desc, id desc
limit sqlc.arg(max_results);

-- name: JobStats :one
-- Totals for jobs queued since a point in time. Queue time is measured only
-- for jobs Airship ran, from queued to started.
select
    count(*)::bigint as jobs,
    count(*) filter (where runner_id is not null)::bigint as airship_jobs,
    count(*) filter (where status = 'queued')::bigint as queued,
    count(*) filter (where status = 'in_progress')::bigint as in_progress,
    count(*) filter (where conclusion = 'success')::bigint as succeeded,
    count(*) filter (where conclusion = 'failure')::bigint as failed,
    coalesce(percentile_cont(0.5) within group (
        order by extract(epoch from started_at - queued_at)
    ) filter (where runner_id is not null and started_at is not null), 0)::float8 as queue_p50_seconds,
    coalesce(percentile_cont(0.9) within group (
        order by extract(epoch from started_at - queued_at)
    ) filter (where runner_id is not null and started_at is not null), 0)::float8 as queue_p90_seconds,
    coalesce(sum(extract(epoch from completed_at - started_at)) filter (
        where runner_id is not null and completed_at is not null and started_at is not null
    ), 0)::float8 as runner_seconds
from jobs
where installation_id = sqlc.arg(installation_id) and queued_at >= sqlc.arg(since);

-- name: LockJob :exec
-- Serializes giving one job a runner, so a retry and a webhook cannot both
-- pass the "no live runner yet" check. (Webhooks already hold the row
-- lock from UpsertJob; retries do not.)
select id from jobs where id = $1 for update;

-- name: JobDailyStats :many
-- Per-day totals for the analytics page: jobs seen, jobs run on Airship,
-- failures, and minutes of runner time.
select
    date_trunc('day', queued_at)::timestamptz as day,
    count(*)::bigint as jobs,
    count(*) filter (where runner_id is not null)::bigint as airship_jobs,
    count(*) filter (where conclusion = 'failure')::bigint as failed,
    coalesce(sum(extract(epoch from completed_at - started_at)) filter (
        where runner_id is not null and completed_at is not null and started_at is not null
    ), 0)::float8 as runner_seconds,
    coalesce(percentile_cont(0.5) within group (
        order by extract(epoch from started_at - queued_at)
    ) filter (where runner_id is not null and started_at is not null), 0)::float8 as queue_p50_seconds
from jobs
where installation_id = sqlc.arg(installation_id) and queued_at >= sqlc.arg(since)
group by 1
order by 1;

-- name: JobRepositoryStats :many
-- Per-repository totals for the analytics page.
select repository_full_name,
    count(*)::bigint as jobs,
    count(*) filter (where runner_id is not null)::bigint as airship_jobs,
    coalesce(sum(extract(epoch from completed_at - started_at)) filter (
        where runner_id is not null and completed_at is not null and started_at is not null
    ), 0)::float8 as runner_seconds
from jobs
where installation_id = sqlc.arg(installation_id) and queued_at >= sqlc.arg(since)
group by repository_full_name
order by runner_seconds desc, jobs desc
limit sqlc.arg(max_results);
