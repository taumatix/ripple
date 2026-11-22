-- Received webhook events, one row per repo, kept until handled (roadmap entry 5).
CREATE TABLE events (
    id              text PRIMARY KEY,
    seq             bigint NOT NULL,
    installation_id bigint NOT NULL,
    kind            text NOT NULL,
    repo            text NOT NULL,
    ref             text NOT NULL,
    files           text[] NOT NULL,
    attempts        integer NOT NULL,
    not_before      timestamptz NOT NULL
);
CREATE INDEX events_by_seq ON events (seq, id);
