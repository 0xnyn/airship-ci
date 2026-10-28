-- +goose Up

-- Microsoft Azure joins the clouds runners launch in: one virtual machine
-- per job, in the customer's subscription.
alter table cloud_credentials drop constraint cloud_credentials_cloud_check;
alter table cloud_credentials add constraint cloud_credentials_cloud_check check (cloud in ('aws', 'gcp', 'daytona', 'azure'));
alter table runners drop constraint runners_cloud_check;
alter table runners add constraint runners_cloud_check check (cloud in ('aws', 'gcp', 'daytona', 'azure'));

-- +goose Down
alter table runners drop constraint runners_cloud_check;
alter table runners add constraint runners_cloud_check check (cloud in ('aws', 'gcp', 'daytona'));
alter table cloud_credentials drop constraint cloud_credentials_cloud_check;
alter table cloud_credentials add constraint cloud_credentials_cloud_check check (cloud in ('aws', 'gcp', 'daytona'));
