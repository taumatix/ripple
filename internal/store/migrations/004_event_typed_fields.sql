-- Typed rename and branch fields on queued events (roadmap entry 56). Additive: events
-- queued before this migration read back with the empty defaults, and a handler falls
-- back to the old use of ref for them.
ALTER TABLE events ADD COLUMN new_repo text NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN default_branch text NOT NULL DEFAULT '';
ALTER TABLE events ADD COLUMN repo_id bigint NOT NULL DEFAULT 0;
