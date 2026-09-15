UPDATE repository_analysis_revisions SET reasoning_effort='medium' WHERE reasoning_effort IS NULL;
ALTER TABLE repository_analysis_revisions ALTER COLUMN reasoning_effort SET NOT NULL;
ALTER TABLE repository_analysis_revisions DROP COLUMN provider;

UPDATE agent_run_attempts SET input_tokens=0 WHERE input_tokens IS NULL;
UPDATE agent_run_attempts SET output_tokens=0 WHERE output_tokens IS NULL;
UPDATE agent_run_attempts SET reasoning_tokens=0 WHERE reasoning_tokens IS NULL;
ALTER TABLE agent_run_attempts ALTER COLUMN input_tokens SET DEFAULT 0;
ALTER TABLE agent_run_attempts ALTER COLUMN input_tokens SET NOT NULL;
ALTER TABLE agent_run_attempts ALTER COLUMN output_tokens SET DEFAULT 0;
ALTER TABLE agent_run_attempts ALTER COLUMN output_tokens SET NOT NULL;
ALTER TABLE agent_run_attempts ALTER COLUMN reasoning_tokens SET DEFAULT 0;
ALTER TABLE agent_run_attempts ALTER COLUMN reasoning_tokens SET NOT NULL;
ALTER TABLE agent_run_attempts DROP COLUMN provider;

UPDATE agent_runs SET reasoning_effort='medium' WHERE reasoning_effort IS NULL;
ALTER TABLE agent_runs ALTER COLUMN reasoning_effort SET NOT NULL;
ALTER TABLE agent_runs DROP COLUMN provider;
