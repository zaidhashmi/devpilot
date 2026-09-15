ALTER TABLE agent_source_accesses
    ADD COLUMN content_digest char(64),
    ADD CONSTRAINT agent_source_accesses_content_digest_check
        CHECK (content_digest IS NULL OR content_digest ~ '^[0-9a-f]{64}$');

CREATE UNIQUE INDEX agent_source_accesses_idempotency_idx
    ON agent_source_accesses(agent_run_id, path, start_line, end_line);
