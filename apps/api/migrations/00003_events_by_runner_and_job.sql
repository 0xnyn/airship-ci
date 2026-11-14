-- +goose Up
-- A runner's or a job's own events, for the dashboard's detail panels.
create index events_runner_idx on events (runner_id, created_at desc, id desc)
    where runner_id is not null;
create index events_job_idx on events (job_id, created_at desc, id desc)
    where job_id is not null;

-- +goose Down
drop index events_job_idx;
drop index events_runner_idx;
