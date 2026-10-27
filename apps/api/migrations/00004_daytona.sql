-- +goose Up

-- Daytona joins AWS and Google Cloud as a cloud runners launch in: one
-- sandbox per job, in the customer's Daytona organization.
alter table cloud_credentials drop constraint cloud_credentials_cloud_check;
alter table cloud_credentials add constraint cloud_credentials_cloud_check check (cloud in ('aws', 'gcp', 'daytona'));
alter table runners drop constraint runners_cloud_check;
alter table runners add constraint runners_cloud_check check (cloud in ('aws', 'gcp', 'daytona'));

-- +goose Down
alter table runners drop constraint runners_cloud_check;
alter table runners add constraint runners_cloud_check check (cloud in ('aws', 'gcp'));
alter table cloud_credentials drop constraint cloud_credentials_cloud_check;
alter table cloud_credentials add constraint cloud_credentials_cloud_check check (cloud in ('aws', 'gcp'));
