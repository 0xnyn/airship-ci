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

-- A GitHub App installation: one GitHub organization or user account. It
-- is the tenant: cloud credentials, repositories, runners and jobs all
-- belong to one.
create table installations (
    id bigint primary key,
    account_id bigint not null,
    account_login text not null,
    account_type text not null check (account_type in ('Organization', 'User')),
    -- Cap on live runners across every repository, so no config file can
    -- run up an unbounded cloud bill.
    max_runners integer not null default 50 check (max_runners between 1 and 10000),
    suspended_at timestamptz,
    -- Set when the app is uninstalled. The row stays so instances still
    -- running in the customer's cloud can be torn down.
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

-- Cloud credentials an admin pasted into the dashboard (or sent through the
-- developer API). The control plane launches runners with them. The secret
-- is encrypted with the api's CREDENTIALS_KEY; identity, account and ARN
-- are what the cloud told us when the secret was verified.
create table cloud_credentials (
    id uuid primary key default uuidv7(),
    installation_id bigint not null references installations (id) on delete cascade,
    cloud text not null check (cloud in ('aws')),
    secret bytea not null,
    -- Public half of the credential, such as an AWS access key ID.
    identity text not null check (char_length(identity) <= 128),
    account_id text check (char_length(account_id) <= 64),
    principal text check (char_length(principal) <= 512),
    -- Where runners launch when a config names no region.
    default_region text not null check (default_region ~ '^[a-z0-9-]{1,64}$'),
    status text not null default 'ok' check (status in ('ok', 'invalid')),
    error text check (char_length(error) <= 2000),
    verified_at timestamptz,
    created_by uuid references users (id) on delete set null,
    created_at timestamptz not null default now(),
    updated_at timestamptz not null default now(),
    unique (installation_id, cloud)
);

-- Developer API tokens: scripts use one to manage an installation's
-- repositories and clouds without a browser. Only a SHA-256 of the token is
-- stored.
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

-- Repositories an admin activated. A .cirun.yml can choose machines in the
-- customer's cloud account, so only activated repositories' files are read.
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
    -- The default branch's .cirun.yml as last read: idle pools come from
    -- it, and the dashboard shows its problems.
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
-- idle: section of a repository's .cirun.yml on its default branch. Pool
-- runners register on that repository only.
create table idle_pools (
    id uuid primary key default uuidv7(),
    installation_id bigint not null references installations (id) on delete cascade,
    repository_id bigint not null references repositories (id) on delete cascade,
    name text not null,
    -- The machines to launch, in fallback order (fleet.want).
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
--   pending    waiting for capacity
--   launching  a launcher holds it: registering with GitHub, calling the cloud
--   running    the cloud accepted the launch; the machine is booting or idle
--   busy       GitHub reported a job in progress on it
--   finished   terminal; finish_reason says why
create table runners (
    id uuid primary key default uuidv7(),
    installation_id bigint not null references installations (id) on delete cascade,
    -- The config runner name it was launched as.
    definition text not null,
    -- Where the definition came from: the repository's .cirun.yml or the
    -- organization's .cirun.global.yml.
    source text not null check (source in ('repository', 'organization')),
    cloud text not null check (cloud in ('aws')),
    -- Null once the credential is deleted; the row stays for history.
    credential_id uuid references cloud_credentials (id) on delete set null,
    region text not null,
    -- The machine to launch (fleet.Spec), fixed when the runner is created.
    spec jsonb not null,
    -- Labels the runner registers with.
    labels text[] not null,
    -- Which entry of the job's plan this runner is.
    attempt integer not null default 0,
    -- Set for warm-pool runners. Without a trigger job they are idle.
    pool_id uuid references idle_pools (id) on delete set null,
    repository_id bigint not null,
    repository_full_name text not null,
    trigger_job_id bigint,
    name text not null unique,
    state text not null default 'pending'
        check (state in ('pending', 'launching', 'running', 'busy', 'finished')),
    finish_reason text check (finish_reason in (
        'completed', 'cancelled', 'idle', 'launch_failed', 'lease_expired',
        'lost', 'max_lifetime', 'deleted'
    )),
    github_runner_id bigint,
    instance_id text,
    error text,
    -- A launching runner whose launcher died is given up after this.
    lease_expires_at timestamptz,
    -- A pending runner is not claimed before this, so a GitHub rate limit
    -- (Retry-After) is honored instead of retried every poll.
    not_before timestamptz,
    created_at timestamptz not null default now(),
    launching_at timestamptz,
    running_at timestamptz,
    busy_at timestamptz,
    finished_at timestamptz,
    -- When the instance was last confirmed present by the cloud, and when
    -- it was found gone.
    instance_seen_at timestamptz,
    instance_gone_at timestamptz,
    -- When the control plane asked the cloud to terminate the instance.
    terminate_requested_at timestamptz,
    -- When the runner was removed from GitHub (null while it may still be
    -- registered). Ephemeral runners that ran a job remove themselves.
    deregistered_at timestamptz,
    -- For provision_via: pull_from_api runners, the sealed boot script the
    -- developer API hands out once (provisioned_at records when).
    provision_secret bytea,
    provisioned_at timestamptz,
    -- For runners with extra_config.cache, the SHA-256 of the token the
    -- machine's cache proxy presents to fetch bucket credentials, and the
    -- bucket it was given.
    cache_token_hash bytea,
    cache_bucket text,
    check ((state = 'finished') = (finish_reason is not null))
);

-- At most one live runner per triggering job; a failed launch can be retried.
create unique index runners_trigger_job_idx on runners (trigger_job_id) where state <> 'finished';
create index runners_trigger_job_all_idx on runners (trigger_job_id);
create index runners_deregister_idx on runners (finished_at)
    where state = 'finished' and github_runner_id is not null and deregistered_at is null;
create index runners_claim_idx on runners (installation_id, created_at) where state = 'pending';
create index runners_active_idx on runners (installation_id, state) where state <> 'finished';
create index runners_installation_created_idx on runners (installation_id, created_at desc);
create index runners_inventory_idx on runners (credential_id, region) where instance_gone_at is null;
create index runners_terminate_idx on runners (finished_at)
    where state = 'finished' and instance_id is not null and instance_gone_at is null;
create index runners_pool_idx on runners (pool_id) where state <> 'finished';
create unique index runners_cache_token_idx on runners (cache_token_hash) where cache_token_hash is not null;

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
    head_sha text,
    -- How Airship serves the job: attempts in fallback order (fleet.Plan).
    -- Null when the job is not Airship's to run.
    plan jsonb,
    -- Set when the job asked for an Airship label but will not be served.
    skip_reason text check (skip_reason in (
        'public_repository', 'unsatisfiable_labels', 'installation_suspended',
        'repository_inactive', 'config_invalid', 'access_denied', 'no_cloud'
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

-- What happened, for the dashboard's Events page: launches and failures,
-- repositories activated, clouds connected, configs that broke.
create table events (
    id uuid primary key default uuidv7(),
    installation_id bigint not null references installations (id) on delete cascade,
    kind text not null check (char_length(kind) <= 64),
    level text not null check (level in ('info', 'warning', 'error')),
    message text not null check (char_length(message) <= 2000),
    repository text,
    runner_id uuid,
    job_id bigint,
    created_at timestamptz not null default now()
);

create index events_installation_idx on events (installation_id, created_at desc, id desc);

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
drop table events;
drop table jobs;
drop function job_status_rank;
drop table runners;
drop table idle_pools;
drop table repositories;
drop table api_tokens;
drop table cloud_credentials;
drop table memberships;
drop table installations;
drop table sessions;
drop table users;
