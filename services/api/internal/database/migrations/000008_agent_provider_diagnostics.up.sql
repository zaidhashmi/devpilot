ALTER TABLE agent_run_attempts
    ADD COLUMN provider_diagnostics jsonb NOT NULL DEFAULT '{}'::jsonb,
    ADD CONSTRAINT agent_run_attempts_provider_diagnostics_object
        CHECK (jsonb_typeof(provider_diagnostics) = 'object'),
    ADD CONSTRAINT agent_run_attempts_provider_diagnostics_bounded
        CHECK (pg_column_size(provider_diagnostics) <= 16384);
