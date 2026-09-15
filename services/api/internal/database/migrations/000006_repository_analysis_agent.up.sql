ALTER TABLE task_runs DROP CONSTRAINT task_runs_current_stage_check;
ALTER TABLE task_runs ADD CONSTRAINT task_runs_current_stage_check CHECK (current_stage IN ('repository_snapshot','repository_inspection','planning','approval','analysis','analysis_approval','finished'));
ALTER TABLE task_runs DROP CONSTRAINT task_runs_status_check;
ALTER TABLE task_runs ADD CONSTRAINT task_runs_status_check CHECK (status IN ('pending','inspecting','analyzing','awaiting_approval','approved','rejected','cancelled','failed','timed_out','completed'));

ALTER TABLE outbox_events DROP CONSTRAINT outbox_events_event_type_check;
ALTER TABLE outbox_events ADD CONSTRAINT outbox_events_event_type_check CHECK (event_type IN ('task_run.start_inspection.v1','task_run.start_analysis.v1','agent_run.execute.v1'));
ALTER TABLE outbox_events DROP CONSTRAINT outbox_events_aggregate_type_check;
ALTER TABLE outbox_events ADD CONSTRAINT outbox_events_aggregate_type_check CHECK (aggregate_type IN ('task_run','agent_run'));

CREATE TABLE agent_runs (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    task_run_id uuid NOT NULL REFERENCES task_runs(id) ON DELETE RESTRICT,
    agent_type text NOT NULL CHECK (agent_type = 'repository_analysis'),
    input_plan_revision_id uuid NOT NULL REFERENCES task_plan_revisions(id) ON DELETE RESTRICT,
    status text NOT NULL CHECK (status IN ('queued','running','succeeded','failed','cancelled','timed_out')),
    model text NOT NULL CHECK (char_length(model) BETWEEN 1 AND 120),
    reasoning_effort text NOT NULL CHECK (reasoning_effort IN ('low','medium','high')),
    prompt_version text NOT NULL CHECK (char_length(prompt_version) BETWEEN 1 AND 120),
    trace_id text CHECK (trace_id IS NULL OR char_length(trace_id) <= 255),
    failure_code text,
    cancellation_requested_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    completed_at timestamptz,
    UNIQUE (task_run_id, agent_type)
);
CREATE INDEX agent_runs_org_created_idx ON agent_runs (organization_id, created_at DESC);
CREATE INDEX agent_runs_active_idx ON agent_runs (status) WHERE status IN ('queued','running');

CREATE TABLE agent_run_attempts (
    id uuid PRIMARY KEY,
    agent_run_id uuid NOT NULL REFERENCES agent_runs(id) ON DELETE RESTRICT,
    attempt_number integer NOT NULL CHECK (attempt_number > 0),
    status text NOT NULL CHECK (status IN ('running','succeeded','failed','cancelled','timed_out')),
    model text NOT NULL,
    input_tokens bigint NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens bigint NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    reasoning_tokens bigint NOT NULL DEFAULT 0 CHECK (reasoning_tokens >= 0),
    model_turns integer NOT NULL DEFAULT 0 CHECK (model_turns >= 0),
    source_tool_calls integer NOT NULL DEFAULT 0 CHECK (source_tool_calls >= 0),
    source_bytes bigint NOT NULL DEFAULT 0 CHECK (source_bytes >= 0),
    failure_code text,
    provider_response_id text CHECK (provider_response_id IS NULL OR char_length(provider_response_id) <= 255),
    created_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    UNIQUE (agent_run_id, attempt_number)
);

CREATE TABLE source_capabilities (
    id uuid PRIMARY KEY,
    token_hash bytea NOT NULL UNIQUE,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    agent_run_id uuid NOT NULL REFERENCES agent_runs(id) ON DELETE RESTRICT,
    task_run_id uuid NOT NULL REFERENCES task_runs(id) ON DELETE RESTRICT,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE RESTRICT,
    commit_sha char(40) NOT NULL CHECK (commit_sha ~ '^[0-9a-f]{40}$'),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    files_accessed integer NOT NULL DEFAULT 0 CHECK (files_accessed >= 0),
    bytes_returned bigint NOT NULL DEFAULT 0 CHECK (bytes_returned >= 0),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX source_capabilities_agent_idx ON source_capabilities (agent_run_id);

CREATE TABLE agent_source_accesses (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    agent_run_id uuid NOT NULL REFERENCES agent_runs(id) ON DELETE RESTRICT,
    task_run_id uuid NOT NULL REFERENCES task_runs(id) ON DELETE RESTRICT,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE RESTRICT,
    commit_sha char(40) NOT NULL CHECK (commit_sha ~ '^[0-9a-f]{40}$'),
    path text NOT NULL CHECK (char_length(path) BETWEEN 1 AND 1024),
    start_line integer NOT NULL CHECK (start_line > 0),
    end_line integer NOT NULL CHECK (end_line >= start_line),
    bytes_returned integer NOT NULL CHECK (bytes_returned >= 0),
    redaction_count integer NOT NULL DEFAULT 0 CHECK (redaction_count >= 0),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX agent_source_accesses_run_idx ON agent_source_accesses (agent_run_id, created_at);

CREATE TABLE repository_analysis_revisions (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    task_run_id uuid NOT NULL REFERENCES task_runs(id) ON DELETE RESTRICT,
    agent_run_id uuid NOT NULL REFERENCES agent_runs(id) ON DELETE RESTRICT,
    revision_number integer NOT NULL CHECK (revision_number > 0),
    source text NOT NULL CHECK (source = 'repository_analysis_agent'),
    model text NOT NULL,
    reasoning_effort text NOT NULL,
    prompt_version text NOT NULL,
    structured_payload jsonb NOT NULL CHECK (jsonb_typeof(structured_payload) = 'object' AND octet_length(structured_payload::text) <= 131072),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (task_run_id, revision_number),
    UNIQUE (agent_run_id),
    UNIQUE (id, task_run_id, revision_number)
);

ALTER TABLE approvals ALTER COLUMN plan_revision_id DROP NOT NULL;
ALTER TABLE approvals ADD COLUMN analysis_revision_id uuid;
ALTER TABLE approvals DROP CONSTRAINT approvals_approval_type_check;
ALTER TABLE approvals ADD CONSTRAINT approvals_approval_type_check CHECK (approval_type IN ('proceed_to_analysis','proceed_to_planning'));
ALTER TABLE approvals ADD CONSTRAINT approvals_subject_check CHECK (
    (approval_type='proceed_to_analysis' AND plan_revision_id IS NOT NULL AND analysis_revision_id IS NULL) OR
    (approval_type='proceed_to_planning' AND plan_revision_id IS NULL AND analysis_revision_id IS NOT NULL)
);
ALTER TABLE approvals ADD CONSTRAINT approvals_analysis_revision_fk FOREIGN KEY (analysis_revision_id, task_run_id, revision) REFERENCES repository_analysis_revisions(id, task_run_id, revision_number) ON DELETE RESTRICT;
