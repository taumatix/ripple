-- Which sources each replica polls and which it has configured (roadmap entry 158), comma
-- separated, so the fleet view can name a source nobody polls. Additive; old rows read as empty.
ALTER TABLE replicas ADD COLUMN polled text NOT NULL DEFAULT '';
ALTER TABLE replicas ADD COLUMN configured text NOT NULL DEFAULT '';
