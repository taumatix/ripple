-- Webhook delivery ids already handled (roadmap entry 64), so a restart or a second replica
-- still drops a redelivery. Additive; rows expire by expires_at and are purged on a timer.
CREATE TABLE deliveries (
    key        text PRIMARY KEY,
    expires_at timestamptz NOT NULL
);
CREATE INDEX deliveries_expires ON deliveries (expires_at);
