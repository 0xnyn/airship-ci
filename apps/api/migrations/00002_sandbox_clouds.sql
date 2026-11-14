-- The sandbox clouds beside Daytona join the cloud name check on
-- credentials and runners: Runloop, E2B and the clouds that speak E2B's
-- protocol (Novita, Lelantos), Namespace, Tenki and Modal.

-- +goose Up
alter table cloud_credentials drop constraint cloud_credentials_cloud_check;
alter table cloud_credentials add constraint cloud_credentials_cloud_check
    check (cloud in ('aws', 'gcp', 'daytona', 'azure', 'runloop', 'e2b', 'novita', 'lelantos', 'namespace', 'tenki', 'modal'));
alter table runners drop constraint runners_cloud_check;
alter table runners add constraint runners_cloud_check
    check (cloud in ('aws', 'gcp', 'daytona', 'azure', 'runloop', 'e2b', 'novita', 'lelantos', 'namespace', 'tenki', 'modal'));

-- +goose Down
-- Fails while credentials or runners for the new clouds exist, which is
-- the point: they cannot be represented below this version.
alter table runners drop constraint runners_cloud_check;
alter table runners add constraint runners_cloud_check
    check (cloud in ('aws', 'gcp', 'daytona', 'azure'));
alter table cloud_credentials drop constraint cloud_credentials_cloud_check;
alter table cloud_credentials add constraint cloud_credentials_cloud_check
    check (cloud in ('aws', 'gcp', 'daytona', 'azure'));
