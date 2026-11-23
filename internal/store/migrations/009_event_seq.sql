-- Store-assigned event sequence (roadmap entry 100): EnqueueSequenced numbers events from this
-- one-row counter inside the insert, so Seq order does not depend on replica clocks. It starts
-- above every Seq already stored and above the clock now, so earlier rows keep sorting first.
-- Additive: EnqueueEvent still stores the Seq it is given.
CREATE TABLE event_seq (n bigint NOT NULL);
INSERT INTO event_seq(n)
SELECT GREATEST(COALESCE((SELECT max(seq) FROM events), 0),
                (extract(epoch from clock_timestamp())::numeric * 1000000000)::bigint);
