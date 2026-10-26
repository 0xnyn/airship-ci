-- name: CreateProfile :one
insert into profiles (
    installation_id, data_plane_id, label, arch, instance_type, image,
    disk_gb, spot, max_runners, allow_public_repos
)
values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
returning *;

-- name: ListProfiles :many
select * from profiles
where installation_id = $1
order by label;

-- name: GetProfile :one
select * from profiles
where installation_id = $1 and id = $2;

-- name: UpdateProfile :one
update profiles
set
    instance_type = coalesce(sqlc.narg(instance_type), instance_type),
    image = case when sqlc.arg(set_image)::boolean then sqlc.narg(image) else image end,
    disk_gb = coalesce(sqlc.narg(disk_gb), disk_gb),
    spot = coalesce(sqlc.narg(spot), spot),
    max_runners = coalesce(sqlc.narg(max_runners), max_runners),
    allow_public_repos = coalesce(sqlc.narg(allow_public_repos), allow_public_repos),
    updated_at = now()
where installation_id = sqlc.arg(installation_id) and id = sqlc.arg(id)
returning *;

-- name: LockProfile :one
-- Blocks runner creation for the profile until the transaction ends.
select * from profiles
where installation_id = $1 and id = $2
for update;

-- name: DeleteProfile :execrows
delete from profiles
where installation_id = $1 and id = $2;

-- name: FindProfilesByLabels :many
select * from profiles
where installation_id = $1 and label = any(sqlc.arg(labels)::text[]);

-- name: LockDataPlaneProfiles :many
-- Serialises claims for one data plane so max_runners holds when an agent
-- runs as more than one replica.
select * from profiles
where data_plane_id = $1
order by id
for update;
