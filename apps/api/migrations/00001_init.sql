-- +goose Up

-- People who sign in to the dashboard with GitHub.
create table users (
    id uuid primary key default uuidv7(),
    github_id bigint not null unique,
    login text not null,
    name text,
    avatar_url text,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

-- Opaque browser sessions. Only a SHA-256 of the cookie value is stored.
create table sessions (
    token_hash bytea primary key,
    user_id uuid not null references users (id) on delete cascade,
    created_at timestamptz not null default now(),
    expires_at timestamptz not null
);

create index sessions_user_id_idx on sessions (user_id);
create index sessions_expires_at_idx on sessions (expires_at);

-- A GitHub App installation: one GitHub organization or user account.
create table installations (
    id bigint primary key,
    account_id bigint not null,
    account_login text not null,
    account_type text not null check (account_type in ('Organization', 'User')),
    suspended_at timestamptz,
    -- Set when the app is uninstalled. The row stays so data plane agents
    -- can still sync and tear down what they launched.
    deleted_at timestamptz,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create unique index installations_account_login_idx on installations (lower(account_login)) where deleted_at is null;

-- Which users may see which installations, as reported by GitHub at sign-in.
create table memberships (
    user_id uuid not null references users (id) on delete cascade,
    installation_id bigint not null references installations (id) on delete cascade,
    -- admin: organization owner (or the account itself); may change settings.
    role text not null check (role in ('admin', 'member')),
    refreshed_at timestamptz not null default now(),
    primary key (user_id, installation_id)
);

create index memberships_installation_id_idx on memberships (installation_id);
create index memberships_refreshed_at_idx on memberships (refreshed_at);

-- A customer-hosted data plane: one agent process (or an HA pair) running in
-- the customer's own infrastructure. It connects out to the control plane
-- with a bearer token; only a SHA-256 of that token is stored.
create table data_planes (
    id uuid primary key default uuidv7(),
    installation_id bigint not null references installations (id) on delete cascade,
    name text not null check (char_length(name) between 1 and 63),
    token_hash bytea not null unique,
    token_hint text not null,
    agent_version text,
    agent_provider text,
    -- Reported by the agent: the cloud region it launches in, if any.
    -- Runner configs select data planes by cloud and region.
    agent_region text check (char_length(agent_region) <= 64),
    -- Cap on live runners across every profile and config on this data
    -- plane, so no config file can run up an unbounded bill.
    max_runners integer not null default 20 check (max_runners between 1 and 10000),
    last_seen_at timestamptz,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    unique (installation_id, name)
);

-- A runner profile maps one runs-on label to a machine shape on a data plane.
create table profiles (
    id uuid primary key default uuidv7(),
    installation_id bigint not null references installations (id) on delete cascade,
    data_plane_id uuid not null references data_planes (id) on delete restrict,
    label text not null check (label ~ '^[a-z0-9][a-z0-9._-]{0,62}$'),
    arch text not null check (arch in ('x64', 'arm64')),
    instance_type text not null check (char_length(instance_type) between 1 and 64),
    image text check (char_length(image) <= 256),
    disk_gb integer not null check (disk_gb between 8 and 16384),
    spot boolean not null default false,
    max_runners integer not null check (max_runners between 1 and 10000),
    -- Public repositories run code from fork pull requests. Running that on
    -- the customer's own network is opt-in.
    allow_public_repos boolean not null default false,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    unique (installation_id, label)
);

create index profiles_data_plane_id_idx on profiles (data_plane_id);

-- Repositories an admin activated for runner config files (.airship.yml
-- and the organization's .airship.global.yml). A config file can choose
-- machines in the customer's account, so only activated repositories'
-- files are read.
-- Developer API tokens: scripts use one to manage an installation's
-- repositories without a browser. Only a SHA-256 of the token is stored.
create table api_tokens (
    id uuid primary key default uuidv7(),
    installation_id bigint not null references installations (id) on delete cascade,
    name text not null check (char_length(name) between 1 and 63),
    token_hash bytea not null unique,
    token_hint text not null,
    created_by uuid references users (id) on delete set null,
    created_at timestamptz not null default now(),
    last_used_at timestamptz
);

create index api_tokens_installation_id_idx on api_tokens (installation_id);

create table repositories (
    id bigint primary key,
    installation_id bigint not null references installations (id) on delete cascade,
    full_name text not null,
    private boolean not null,
    default_branch text not null,
    -- Public repositories run code from fork pull requests on the
    -- customer's machines; serving one is a separate, explicit choice.
    allow_public boolean not null default false,
    activated_by uuid references users (id) on delete set null,
    activated_at timestamptz not null default now(),
    -- The default branch's .airship.yml as last read: idle pools come
    -- from it, and the dashboard shows its problems.
    config_sha text,
    config_error text,
    config_checked_at timestamptz,
    -- Set when a push may have changed the config; the next refresh pass
    -- reads it again.
    config_stale boolean not null default true
);

create index repositories_installation_idx on repositories (installation_id, lower(full_name));
create index repositories_stale_idx on repositories (config_checked_at) where config_stale;

-- A warm pool: runners kept booted and registered ahead of demand, from an
-- idle: section of a repository's .airship.yml on its default branch.
-- Pool runners register on that repository only.
create table idle_pools (
    id uuid primary key default uuidv7(),
    installation_id bigint not null references installations (id) on delete cascade,
    repository_id bigint not null references repositories (id) on delete cascade,
    name text not null,
    -- The attempts to launch with, in fallback order (fleet.Plan).
    plan jsonb not null,
    -- The idle: section (fleet.IdleSpec): fixed, scheduled or usage-based.
    idle jsonb not null,
    -- Usage-based pools stay warm until this time after a runner from the
    -- same definition is allocated a job.
    usage_until timestamptz,
    source_sha text not null,
    updated_at timestamptz not null default now(),
    unique (installation_id, repository_id, name)
);

-- One row per runner Airship has created. A runner is created for a queued
-- job, but GitHub may hand it any queued job in the same repository with
-- matching labels, so jobs.runner_id (not trigger_job_id) records which job a
-- runner actually ran.
--
-- States:
--   pending   waiting for capacity and for the agent to pick it up
--   assigned  a JIT config was minted and handed to the agent
--   running   the agent reported a live instance for it
--   busy      GitHub reported a job in progress on it
--   finished  terminal; finish_reason says why
create table runners (
    id uuid primary key default uuidv7(),
    installation_id bigint not null references installations (id) on delete cascade,
    -- Null once the profile is deleted; the row stays so the runner can
    -- still be removed from GitHub and its history is kept.
    profile_id uuid references profiles (id) on delete set null,
    -- The profile label or config runner name it was launched as.
    profile_label text not null,
    -- Where the definition came from: a dashboard profile, the
    -- repository's .airship.yml or the organization's .airship.global.yml.
    source text not null default 'profile' check (source in ('profile', 'repository', 'organization')),
    -- The machine to launch (fleet.Spec), fixed when the runner is created.
    spec jsonb not null,
    -- Labels the runner registers with.
    labels text[] not null,
    -- Which entry of the job's plan this runner is.
    attempt integer not null default 0,
    -- Set for warm-pool runners. Without a trigger job they are idle.
    pool_id uuid references idle_pools (id) on delete set null,
    data_plane_id uuid not null references data_planes (id) on delete cascade,
    repository_id bigint not null,
    repository_full_name text not null,
    trigger_job_id bigint,
    name text not null unique,
    state text not null default 'pending'
        check (state in ('pending', 'assigned', 'running', 'busy', 'finished')),
    finish_reason text check (finish_reason in (
        'completed', 'cancelled', 'idle', 'launch_failed', 'lease_expired',
        'lost', 'max_lifetime', 'deleted'
    )),
    github_runner_id bigint,
    instance_id text,
    error text,
    lease_expires_at timestamptz,
    -- A pending runner is not claimed before this, so a GitHub rate limit
    -- (Retry-After) is honored instead of retried every poll.
    not_before timestamptz,
    created_at timestamptz not null default now(),
    assigned_at timestamptz,
    running_at timestamptz,
    busy_at timestamptz,
    finished_at timestamptz,
    instance_gone_at timestamptz,
    -- When the runner was removed from GitHub (null while it may still be
    -- registered). Ephemeral runners that ran a job remove themselves.
    deregistered_at timestamptz,
    check ((state = 'finished') = (finish_reason is not null))
);

-- At most one live runner per triggering job; a failed launch can be retried.
create unique index runners_trigger_job_idx on runners (trigger_job_id) where state <> 'finished';
create index runners_trigger_job_all_idx on runners (trigger_job_id);
create index runners_deregister_idx on runners (finished_at)
    where state = 'finished' and github_runner_id is not null and deregistered_at is null;
create index runners_claim_idx on runners (data_plane_id, created_at) where state = 'pending';
create index runners_active_idx on runners (data_plane_id, state) where state <> 'finished';
create index runners_installation_created_idx on runners (installation_id, created_at desc);
create index runners_unreaped_idx on runners (data_plane_id) where instance_gone_at is null;
create index runners_pool_idx on runners (pool_id) where state <> 'finished';

-- Orders job statuses so webhooks delivered out of order never move a job
-- backwards (queued < in_progress < completed).
-- +goose StatementBegin
create function job_status_rank(status text) returns integer
language sql immutable strict parallel safe
return case status when 'queued' then 0 when 'in_progress' then 1 when 'completed' then 2 end;
-- +goose StatementEnd

-- workflow_job events, folded into one row per GitHub job.
create table jobs (
    id bigint primary key,
    installation_id bigint not null references installations (id) on delete cascade,
    repository_id bigint not null,
    repository_full_name text not null,
    run_id bigint not null,
    run_attempt integer not null default 1,
    workflow_name text,
    name text not null,
    head_branch text,
    html_url text,
    labels text[] not null,
    status text not null check (status in ('queued', 'in_progress', 'completed')),
    conclusion text,
    profile_id uuid references profiles (id) on delete set null,
    head_sha text,
    -- How Airship serves the job: attempts in fallback order (fleet.Plan).
    -- Null when the job is not Airship's to run.
    plan jsonb,
    -- Set when the job asked for an Airship label but will not be served.
    skip_reason text check (skip_reason in (
        'public_repository', 'unsatisfiable_labels', 'ambiguous_labels', 'installation_suspended',
        'repository_inactive', 'config_invalid', 'access_denied', 'no_data_plane'
    )),
    -- Explains skip_reason, such as the config file's error.
    skip_detail text check (char_length(skip_detail) <= 2000),
    runner_id uuid references runners (id) on delete set null,
    runner_name text,
    queued_at timestamptz not null,
    started_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now()
);

create index jobs_installation_queued_idx on jobs (installation_id, queued_at desc, id desc);
create index jobs_stale_queued_idx on jobs (queued_at) where status = 'queued' and plan is not null;
create index jobs_runner_id_idx on jobs (runner_id);

-- GitHub never redelivers a failed webhook on its own. The redeliverer
-- scans the app's delivery log down to the newest delivery it has already
-- seen (the watermark), records failures here, and asks GitHub to resend
-- them with backoff until one attempt succeeds.
create table webhook_scan (
    id boolean primary key default true check (id),
    watermark bigint not null
);

create table failed_deliveries (
    guid text primary key,
    delivery_id bigint not null,
    event text not null,
    attempts integer not null default 0,
    next_attempt_at timestamptz not null default now(),
    first_failed_at timestamptz not null default now()
);

create index failed_deliveries_due_idx on failed_deliveries (next_attempt_at);

-- +goose Down
drop table failed_deliveries;
drop table webhook_scan;
drop table jobs;
drop function job_status_rank;
drop table runners;
drop table idle_pools;
drop table repositories;
drop table api_tokens;
drop table profiles;
drop table data_planes;
drop table memberships;
drop table installations;
drop table sessions;
drop table users;
