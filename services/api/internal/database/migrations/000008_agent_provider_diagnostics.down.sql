ALTER TABLE agent_run_attempts
    DROP CONSTRAINT agent_run_attempts_provider_diagnostics_bounded,
    DROP CONSTRAINT agent_run_attempts_provider_diagnostics_object,
    DROP COLUMN provider_diagnostics;
