-- Dead letters (roadmap entry 8): an event that used up its attempts is kept with its last
-- error and times instead of deleted. Additive: existing rows get the defaults and read as live.
ALTER TABLE events ADD COLUMN last_error text NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN last_attempt_at timestamptz;
ALTER TABLE events ADD COLUMN dead_at timestamptz;
CREATE INDEX events_dead ON events (dead_at, id) WHERE dead_at IS NOT NULL;
