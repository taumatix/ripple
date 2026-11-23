-- The dependency dashboard lists one repo's open (pending or failed) jobs.
CREATE INDEX jobs_open_by_repo ON jobs (repo) WHERE state IN ('pending', 'failed');
