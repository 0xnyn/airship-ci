-- name: UpsertCloudCredential :one
-- Saving credentials for a cloud that already has some replaces them in
-- place, so runners in flight keep their credential_id.
insert into cloud_credentials (
    installation_id, cloud, secret, identity, account_id, principal, default_region,
    status, error, verified_at, created_by
)
values ($1, $2, $3, $4, $5, $6, $7, 'ok', null, now(), $8)
on conflict (installation_id, cloud) do update
set secret = excluded.secret, identity = excluded.identity, account_id = excluded.account_id,
    principal = excluded.principal, default_region = excluded.default_region,
    status = 'ok', error = null, verified_at = now(), created_by = excluded.created_by,
    updated_at = now()
returning *;

-- name: GetCloudCredential :one
select * from cloud_credentials where installation_id = $1 and cloud = $2;

-- name: GetCloudCredentialByID :one
select * from cloud_credentials where id = $1;

-- name: ListCloudCredentials :many
select * from cloud_credentials where installation_id = $1 order by cloud;

-- name: DeleteCloudCredential :execrows
delete from cloud_credentials where installation_id = $1 and cloud = $2;

-- name: SetCloudCredentialStatus :execrows
-- Records whether a credential still works, as long as it is still the
-- sealed secret that was checked: a reconnect in between wins.
update cloud_credentials
set status = sqlc.arg(status), error = sqlc.narg(error), updated_at = now()
where id = sqlc.arg(id) and secret = sqlc.arg(secret) and status <> sqlc.arg(status);

-- name: SetCloudCredentialDefaultRegion :one
update cloud_credentials set default_region = $3, updated_at = now()
where installation_id = $1 and cloud = $2
returning *;
