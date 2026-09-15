ALTER TABLE agent_runs ADD COLUMN provider text NOT NULL DEFAULT 'openai' CHECK (provider IN ('openai','ollama'));
ALTER TABLE agent_runs ALTER COLUMN provider DROP DEFAULT;
ALTER TABLE agent_runs ALTER COLUMN reasoning_effort DROP NOT NULL;

ALTER TABLE agent_run_attempts ADD COLUMN provider text NOT NULL DEFAULT 'openai' CHECK (provider IN ('openai','ollama'));
ALTER TABLE agent_run_attempts ALTER COLUMN provider DROP DEFAULT;
ALTER TABLE agent_run_attempts ALTER COLUMN input_tokens DROP NOT NULL;
ALTER TABLE agent_run_attempts ALTER COLUMN input_tokens DROP DEFAULT;
ALTER TABLE agent_run_attempts ALTER COLUMN output_tokens DROP NOT NULL;
ALTER TABLE agent_run_attempts ALTER COLUMN output_tokens DROP DEFAULT;
ALTER TABLE agent_run_attempts ALTER COLUMN reasoning_tokens DROP NOT NULL;
ALTER TABLE agent_run_attempts ALTER COLUMN reasoning_tokens DROP DEFAULT;

ALTER TABLE repository_analysis_revisions ADD COLUMN provider text NOT NULL DEFAULT 'openai' CHECK (provider IN ('openai','ollama'));
ALTER TABLE repository_analysis_revisions ALTER COLUMN provider DROP DEFAULT;
ALTER TABLE repository_analysis_revisions ALTER COLUMN reasoning_effort DROP NOT NULL;
