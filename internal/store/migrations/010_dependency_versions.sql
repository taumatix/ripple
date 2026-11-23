-- One package at two versions in one file (actions/checkout@v4 and @v5.0.0 in one workflow, two
-- FROM lines naming one image) is two rows, so each can be updated on its own. Additive: every
-- row that fit the old key fits the new one.
ALTER TABLE dependencies DROP CONSTRAINT dependencies_pkey;
ALTER TABLE dependencies ADD PRIMARY KEY (repo, file, datasource, package, version);
