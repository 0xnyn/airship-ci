-- +goose Up

-- When an idle pool runner was pointed at a queued job. The idle reaper
-- measures a reserved runner's wait from here, not from its launch, so a
-- runner that has been warm for hours is not written off as idle the
-- moment a job reserves it.
alter table runners add column reserved_at timestamptz;

-- +goose Down
alter table runners drop column reserved_at;
