DROP INDEX agent_source_accesses_idempotency_idx;

ALTER TABLE agent_source_accesses
    DROP CONSTRAINT agent_source_accesses_content_digest_check,
    DROP COLUMN content_digest;
