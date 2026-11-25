-- The head commit and body hash of the pull request ripple opened for a job (roadmap entry 94),
-- so a later pass can notice an edit no host reports. Additive and safe on a table with rows:
-- the default fills existing jobs with '' (no record), which the comparison reads as "unknown".
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS head_sha text NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS body_hash text NOT NULL DEFAULT '';
