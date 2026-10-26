-- name: CreateAPIToken :one
insert into api_tokens (installation_id, name, token_hash, token_hint, created_by)
values ($1, $2, $3, $4, $5)
returning *;

-- name: ListAPITokens :many
select * from api_tokens
where installation_id = $1
order by created_at desc;

-- name: DeleteAPIToken :execrows
delete from api_tokens
where installation_id = $1 and id = $2;

-- name: CountAPITokens :one
select count(*) from api_tokens where installation_id = $1;

-- name: AuthenticateAPIToken :one
-- Tokens of uninstalled or suspended installations do not authenticate.
select sqlc.embed(api_tokens), sqlc.embed(installations)
from api_tokens
join installations on installations.id = api_tokens.installation_id
where api_tokens.token_hash = $1
    and installations.deleted_at is null
    and installations.suspended_at is null;

-- name: TouchAPIToken :exec
-- At most once a minute, so a busy script does not write on every call.
update api_tokens
set last_used_at = now()
where id = $1 and (last_used_at is null or last_used_at < now() - interval '1 minute');
