-- +goose Up

-- Google Cloud joins AWS as a cloud runners launch in.
alter table cloud_credentials drop constraint cloud_credentials_cloud_check;
alter table cloud_credentials add constraint cloud_credentials_cloud_check check (cloud in ('aws', 'gcp'));
alter table runners drop constraint runners_cloud_check;
alter table runners add constraint runners_cloud_check check (cloud in ('aws', 'gcp'));

-- launch_unconfirmed: the cloud may or may not have created the machine
-- (the answer to the launch was lost). Like lease_expired, the runner is
-- revived if the inventory scan lists its instance and marked gone after
-- the grace period otherwise.
alter table runners drop constraint runners_finish_reason_check;
alter table runners add constraint runners_finish_reason_check check (finish_reason in (
    'completed', 'cancelled', 'idle', 'launch_failed', 'launch_unconfirmed', 'lease_expired',
    'lost', 'max_lifetime', 'deleted'
));

-- +goose Down
alter table runners drop constraint runners_finish_reason_check;
alter table runners add constraint runners_finish_reason_check check (finish_reason in (
    'completed', 'cancelled', 'idle', 'launch_failed', 'lease_expired',
    'lost', 'max_lifetime', 'deleted'
));
alter table runners drop constraint runners_cloud_check;
alter table runners add constraint runners_cloud_check check (cloud in ('aws'));
alter table cloud_credentials drop constraint cloud_credentials_cloud_check;
alter table cloud_credentials add constraint cloud_credentials_cloud_check check (cloud in ('aws'));
