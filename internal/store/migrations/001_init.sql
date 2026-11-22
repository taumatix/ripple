CREATE TABLE repos (
    full_name       text PRIMARY KEY,
    installation_id bigint NOT NULL,
    default_branch  text NOT NULL,
    config          bytea
);

CREATE TABLE dependencies (
    repo       text NOT NULL,
    file       text NOT NULL,
    manager    text NOT NULL,
    datasource text NOT NULL,
    package    text NOT NULL,
    version    text NOT NULL,
    -- the package without its major-version suffix: v60 and v61 of a module share a line.
    line       text NOT NULL,
    PRIMARY KEY (repo, file, datasource, package)
);
-- The reverse index: which repos import any major version of package X.
CREATE INDEX dependencies_by_line ON dependencies (datasource, line);

CREATE TABLE releases (
    datasource  text NOT NULL,
    package     text NOT NULL,
    version     text NOT NULL,
    released_at timestamptz NOT NULL,
    PRIMARY KEY (datasource, package, version)
);

CREATE TABLE jobs (
    key         text PRIMARY KEY,
    repo        text NOT NULL,
    datasource  text NOT NULL,
    package     text NOT NULL,
    version     text NOT NULL,
    released_at timestamptz NOT NULL,
    state       text NOT NULL,
    not_before  timestamptz NOT NULL,
    attempts    integer NOT NULL,
    pr_url      text NOT NULL,
    reason      text NOT NULL
);
CREATE INDEX jobs_due ON jobs (not_before) WHERE state = 'pending';

CREATE TABLE cursors (
    name  text PRIMARY KEY,
    value text NOT NULL
);
