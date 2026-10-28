-- Applied when the local database is created: the first `supabase start`,
-- and every `supabase db reset` (`make supabase-reset`).
--
-- This mirrors step 2 of docs/deploy/stage-0/3-supabase.md. Airship's
-- migrations run as their own role, so it owns every table it creates and
-- the roles Supabase's Data API would use are granted nothing.
-- createdb is local only: the test harness creates one database per test
-- (internal/testsupport/pgtest). The hosted role has no such privilege and
-- does not need one.
do $$
begin
    if not exists (select from pg_roles where rolname = 'airship') then
        create role airship with login createdb password 'airship';
    end if;
end $$;

grant usage, create on schema public to airship;
