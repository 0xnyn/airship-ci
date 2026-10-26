-- name: UpsertUser :one
insert into users (github_id, login, name, avatar_url)
values ($1, $2, $3, $4)
on conflict (github_id) do update
set login = excluded.login,
    name = excluded.name,
    avatar_url = excluded.avatar_url,
    updated_at = now()
returning *;

-- name: CreateSession :exec
insert into sessions (token_hash, user_id, expires_at)
values ($1, $2, $3);

-- name: GetSessionUser :one
select users.*
from sessions
join users on users.id = sessions.user_id
where sessions.token_hash = $1 and sessions.expires_at > now();

-- name: DeleteSession :exec
delete from sessions where token_hash = $1;

-- name: DeleteExpiredSessions :execrows
delete from sessions where expires_at <= now();

-- name: UpsertInstallation :one
insert into installations (id, account_id, account_login, account_type, suspended_at)
values ($1, $2, $3, $4, $5)
on conflict (id) do update
set account_id = excluded.account_id,
    account_login = excluded.account_login,
    account_type = excluded.account_type,
    suspended_at = excluded.suspended_at,
    deleted_at = null,
    updated_at = now()
returning *;

-- name: SetInstallationSuspended :exec
update installations set suspended_at = $2, updated_at = now() where id = $1;

-- name: MarkInstallationDeleted :exec
update installations set deleted_at = now(), updated_at = now() where id = $1;

-- name: DeleteInstallationMemberships :exec
delete from memberships where installation_id = $1;

-- name: GetInstallation :one
select * from installations where id = $1;

-- name: UpsertMembership :exec
insert into memberships (user_id, installation_id, role)
values ($1, $2, $3)
on conflict (user_id, installation_id) do update
set role = excluded.role, refreshed_at = now();

-- name: DeleteOtherMemberships :exec
-- Drops memberships GitHub no longer reports for the user.
delete from memberships
where user_id = sqlc.arg(user_id) and not (installation_id = any(sqlc.arg(keep_ids)::bigint[]));

-- name: ListUserInstallations :many
select sqlc.embed(installations), memberships.role
from memberships
join installations on installations.id = memberships.installation_id
where memberships.user_id = $1 and installations.deleted_at is null
order by lower(installations.account_login);

-- name: GetUserInstallationByLogin :one
select sqlc.embed(installations), memberships.role
from memberships
join installations on installations.id = memberships.installation_id
where memberships.user_id = $1 and installations.deleted_at is null
    and lower(installations.account_login) = lower(sqlc.arg(login));

-- name: ListLiveInstallations :many
select * from installations where deleted_at is null order by id;

-- name: ClaimStaleMemberships :many
-- Memberships whose role has not been checked with GitHub since before.
-- Claiming bumps refreshed_at, so concurrent API replicas share the work.
update memberships
set refreshed_at = now()
from users, installations
where (memberships.user_id, memberships.installation_id) in (
        select candidates.user_id, candidates.installation_id from memberships as candidates
        where candidates.refreshed_at < sqlc.arg(before)::timestamptz
        order by candidates.refreshed_at
        limit sqlc.arg(max_results)
        for update skip locked
    )
    and users.id = memberships.user_id
    and installations.id = memberships.installation_id
returning memberships.user_id, memberships.installation_id, memberships.role,
    users.github_id, users.login, installations.account_id, installations.account_login,
    installations.account_type, installations.deleted_at;

-- name: SetMembershipRole :exec
update memberships set role = $3
where user_id = $1 and installation_id = $2;

-- name: DeleteMembership :exec
delete from memberships where user_id = $1 and installation_id = $2;

-- name: SetInstallationMaxRunners :one
update installations set max_runners = $2, updated_at = now() where id = $1 returning *;

-- name: ListInstallationMembers :many
select sqlc.embed(users), memberships.role, memberships.refreshed_at
from memberships
join users on users.id = memberships.user_id
where memberships.installation_id = $1
order by memberships.role, lower(users.login);

-- name: ListStaleOrganizationConfigs :many
-- Organizations whose .cirun.global.yml should be read again for idle
-- runners: pushed to, or not read for a while.
select * from installations
where deleted_at is null and account_type = 'Organization'
    and (org_config_checked_at is null
        or (org_config_stale and org_config_checked_at < sqlc.arg(retry_before)::timestamptz)
        or org_config_checked_at < sqlc.arg(checked_before)::timestamptz)
order by org_config_checked_at nulls first
limit sqlc.arg(max_results);

-- name: MarkOrganizationConfigStale :exec
update installations set org_config_stale = true, org_config_checked_at = null where id = $1;

-- name: TouchOrganizationConfig :exec
update installations set org_config_checked_at = now() where id = $1;

-- name: SetOrganizationConfig :exec
update installations
set org_config_sha = sqlc.narg(sha), org_config_error = sqlc.narg(error),
    org_config_checked_at = now(), org_config_stale = false
where id = sqlc.arg(id);
