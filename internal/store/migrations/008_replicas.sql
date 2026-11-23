-- Live replicas (roadmap entry 143): each replica beats on its own row, which lapses after
-- a ttl, so the service can count replicas without a lease listing. Additive. Times are the
-- database's now().
CREATE TABLE replicas (
    id          text PRIMARY KEY,
    takeovers   bigint NOT NULL,
    feed_poller boolean NOT NULL,
    seen        timestamptz NOT NULL,
    expires     timestamptz NOT NULL
);
