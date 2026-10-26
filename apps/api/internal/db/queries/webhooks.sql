-- name: GetWebhookWatermark :one
select watermark from webhook_scan where id;

-- name: SetWebhookWatermark :exec
insert into webhook_scan (id, watermark) values (true, $1)
on conflict (id) do update set watermark = greatest(webhook_scan.watermark, excluded.watermark);

-- name: RecordFailedDelivery :exec
-- A failed attempt. A delivery already being retried keeps its schedule.
insert into failed_deliveries (guid, delivery_id, event)
values ($1, $2, $3)
on conflict (guid) do update set delivery_id = greatest(failed_deliveries.delivery_id, excluded.delivery_id);

-- name: ResolveDeliveries :exec
-- Deliveries that have since succeeded.
delete from failed_deliveries where guid = any(sqlc.arg(guids)::text[]);

-- name: ClaimDueDeliveries :many
-- Due retries; the next one is scheduled with exponential backoff, capped
-- at an hour. Deliveries that keep failing are dropped after max_attempts.
update failed_deliveries
set attempts = attempts + 1,
    next_attempt_at = now() + least(interval '1 hour', interval '1 minute' * power(2, attempts))
where guid in (
    select candidates.guid from failed_deliveries as candidates
    where candidates.next_attempt_at <= now() and candidates.attempts < sqlc.arg(max_attempts)::integer
    order by candidates.next_attempt_at
    limit sqlc.arg(max_results)
    for update skip locked
)
returning *;

-- name: DeleteOldDeliveries :exec
-- GitHub keeps deliveries for three days; nothing older can be resent.
delete from failed_deliveries where first_failed_at < sqlc.arg(before)::timestamptz;
