-- name: CreateDataPlane :one
insert into data_planes (installation_id, name, token_hash, token_hint)
values ($1, $2, $3, $4)
returning *;

-- name: ListDataPlanes :many
select * from data_planes
where installation_id = $1
order by lower(name);

-- name: GetDataPlane :one
select * from data_planes
where installation_id = $1 and id = $2;

-- name: RotateDataPlaneToken :one
update data_planes
set token_hash = $3, token_hint = $4, updated_at = now()
where installation_id = $1 and id = $2
returning *;

-- name: DeleteDataPlane :execrows
delete from data_planes
where installation_id = $1 and id = $2;

-- name: CountDataPlaneProfiles :one
select count(*) from profiles where data_plane_id = $1;

-- name: AuthenticateDataPlane :one
select sqlc.embed(data_planes), installations.deleted_at as installation_deleted_at,
    installations.suspended_at as installation_suspended_at
from data_planes
join installations on installations.id = data_planes.installation_id
where data_planes.token_hash = $1;

-- name: TouchDataPlane :one
-- Returns the database's clock, which runner timestamps are compared to.
update data_planes
set last_seen_at = now(), agent_version = $2, agent_provider = $3
where id = $1
returning last_seen_at::timestamptz;
