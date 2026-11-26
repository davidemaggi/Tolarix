-- When the schedule of a flow last "started counting": set when the flow is
-- created, when its schedule, timezone or switches change, and when a due run
-- is claimed. The next run is the first schedule time after it. Empty (rows
-- from before this migration) falls back to updated_at.

-- +goose Up
ALTER TABLE flows ADD COLUMN schedule_anchor TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE flows DROP COLUMN schedule_anchor;
