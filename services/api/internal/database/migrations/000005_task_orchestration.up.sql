CREATE TABLE engineering_tasks (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE RESTRICT,
    created_by_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 180),
    objective text NOT NULL CHECK (char_length(objective) BETWEEN 1 AND 8000),
    status text NOT NULL CHECK (status IN ('active','completed','cancelled','failed')),
    created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(), closed_at timestamptz
);
CREATE INDEX engineering_tasks_org_created_idx ON engineering_tasks (organization_id, created_at DESC);

CREATE TABLE task_runs (
    id uuid PRIMARY KEY,
    engineering_task_id uuid NOT NULL REFERENCES engineering_tasks(id) ON DELETE RESTRICT,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE RESTRICT,
    workspace_id uuid UNIQUE REFERENCES workspaces(id) ON DELETE RESTRICT,
    run_number integer NOT NULL CHECK (run_number > 0),
    requested_ref text NOT NULL CHECK (char_length(requested_ref) BETWEEN 1 AND 255),
    resolved_commit_sha char(40) NOT NULL CHECK (resolved_commit_sha ~ '^[0-9a-f]{40}$'),
    status text NOT NULL CHECK (status IN ('pending','inspecting','awaiting_approval','approved','rejected','cancelled','failed','timed_out','completed')),
    current_stage text NOT NULL CHECK (current_stage IN ('repository_snapshot','repository_inspection','planning','approval','finished')),
    created_by_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    cancellation_requested_at timestamptz, failure_code text,
    created_at timestamptz NOT NULL DEFAULT now(), started_at timestamptz, completed_at timestamptz,
    UNIQUE (engineering_task_id, run_number)
);
CREATE INDEX task_runs_org_created_idx ON task_runs (organization_id, created_at DESC);
CREATE INDEX task_runs_active_idx ON task_runs (status) WHERE status IN ('pending','inspecting','awaiting_approval');

CREATE TABLE task_plan_revisions (
    id uuid PRIMARY KEY,
    task_run_id uuid NOT NULL REFERENCES task_runs(id) ON DELETE RESTRICT,
    revision_number integer NOT NULL CHECK (revision_number > 0),
    source text NOT NULL CHECK (source = 'deterministic'),
    summary text NOT NULL CHECK (char_length(summary) BETWEEN 1 AND 2000),
    structured_payload jsonb NOT NULL CHECK (jsonb_typeof(structured_payload) = 'object' AND octet_length(structured_payload::text) <= 32768),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (task_run_id, revision_number), UNIQUE (id, task_run_id, revision_number)
);

CREATE TABLE approvals (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    task_run_id uuid NOT NULL REFERENCES task_runs(id) ON DELETE RESTRICT,
    plan_revision_id uuid NOT NULL,
    approval_type text NOT NULL CHECK (approval_type = 'proceed_to_analysis'),
    revision integer NOT NULL CHECK (revision > 0),
    status text NOT NULL CHECK (status IN ('pending','approved','rejected','cancelled','superseded')),
    requested_at timestamptz NOT NULL DEFAULT now(), decided_at timestamptz,
    decided_by_user_id uuid REFERENCES users(id) ON DELETE RESTRICT,
    decision_comment text CHECK (decision_comment IS NULL OR char_length(decision_comment) <= 1000),
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (plan_revision_id, task_run_id, revision) REFERENCES task_plan_revisions(id, task_run_id, revision_number) ON DELETE RESTRICT,
    UNIQUE (task_run_id, approval_type, revision)
);
CREATE UNIQUE INDEX approvals_one_pending_type_idx ON approvals (task_run_id, approval_type) WHERE status='pending';

CREATE TABLE outbox_events (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    aggregate_type text NOT NULL CHECK (aggregate_type = 'task_run'),
    aggregate_id uuid NOT NULL,
    event_type text NOT NULL CHECK (event_type = 'task_run.start_inspection.v1'),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object' AND octet_length(payload::text) <= 16384),
    created_at timestamptz NOT NULL DEFAULT now(), available_at timestamptz NOT NULL DEFAULT now(),
    claimed_at timestamptz, claimed_by text, completed_at timestamptz,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0), last_error_code text,
    UNIQUE (event_type, aggregate_id)
);
CREATE INDEX outbox_available_idx ON outbox_events (available_at, created_at) WHERE completed_at IS NULL;
