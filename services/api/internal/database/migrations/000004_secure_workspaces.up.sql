CREATE TABLE workspaces (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE RESTRICT,
    requested_ref text NOT NULL CHECK (char_length(requested_ref) BETWEEN 1 AND 255),
    resolved_commit_sha char(40) NOT NULL CHECK (resolved_commit_sha ~ '^[0-9a-f]{40}$'),
    status text NOT NULL CHECK (status IN ('pending','acquiring','inspecting','succeeded','failed','cancelled','timed_out')),
    created_by_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    inspection_artifact jsonb,
    failure_code text,
    cancellation_requested_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    started_at timestamptz,
    completed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'succeeded') = (inspection_artifact IS NOT NULL)),
    CHECK (inspection_artifact IS NULL OR jsonb_typeof(inspection_artifact) = 'object')
);

CREATE INDEX workspaces_org_created_idx ON workspaces (organization_id, created_at DESC);
CREATE INDEX workspaces_repository_created_idx ON workspaces (repository_id, created_at DESC);
CREATE INDEX workspaces_active_idx ON workspaces (status) WHERE status IN ('pending','acquiring','inspecting');

CREATE TABLE workspace_attempts (
    id uuid PRIMARY KEY,
    workspace_id uuid NOT NULL REFERENCES workspaces(id) ON DELETE RESTRICT,
    attempt_number integer NOT NULL CHECK (attempt_number > 0),
    status text NOT NULL CHECK (status IN ('acquiring','inspecting','succeeded','failed','cancelled','timed_out')),
    failure_code text,
    started_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    UNIQUE (workspace_id, attempt_number)
);

