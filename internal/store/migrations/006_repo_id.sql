-- The host's immutable repository id on the repo record (roadmap entry 73). Additive:
-- repos registered before this migration read back 0 and adopt the id from the next
-- signed push that carries one.
ALTER TABLE repos ADD COLUMN repo_id bigint NOT NULL DEFAULT 0;
