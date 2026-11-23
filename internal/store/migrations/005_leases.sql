-- Leases (roadmap entry 7): replicas that share this database claim a name (an event, a
-- repo's job run) for a while and renew it; one that dies stops renewing and another
-- takes the name over. Additive. Expiry is always judged by the database's own now().
CREATE TABLE leases (
    name    text PRIMARY KEY,
    owner   text NOT NULL,
    token   bigint NOT NULL,
    expires timestamptz NOT NULL
);
