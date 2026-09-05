CREATE TABLE github_installations (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    github_installation_id bigint NOT NULL UNIQUE CHECK (github_installation_id > 0),
    github_account_id bigint NOT NULL CHECK (github_account_id > 0),
    github_account_login text NOT NULL CHECK (char_length(github_account_login) BETWEEN 1 AND 255),
    github_account_type text NOT NULL CHECK (github_account_type IN ('User', 'Organization')),
    repository_selection text NOT NULL CHECK (repository_selection IN ('all', 'selected')),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'suspended', 'removed')),
    suspended_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX github_installations_active_org_idx
    ON github_installations (organization_id) WHERE status <> 'removed';

CREATE TABLE repositories (
    id uuid PRIMARY KEY,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    github_installation_id uuid NOT NULL REFERENCES github_installations(id) ON DELETE RESTRICT,
    github_repository_id bigint NOT NULL CHECK (github_repository_id > 0),
    owner text NOT NULL,
    name text NOT NULL,
    full_name text NOT NULL,
    default_branch text NOT NULL,
    private boolean NOT NULL,
    archived boolean NOT NULL DEFAULT false,
    disabled boolean NOT NULL DEFAULT false,
    available boolean NOT NULL DEFAULT true,
    html_url text NOT NULL,
    github_updated_at timestamptz,
    last_synced_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (github_installation_id, github_repository_id),
    UNIQUE (organization_id, github_repository_id)
);

CREATE INDEX repositories_org_available_idx
    ON repositories (organization_id, full_name) WHERE available = true;

CREATE TABLE github_installation_states (
    id uuid PRIMARY KEY,
    token_hash bytea NOT NULL UNIQUE,
    organization_id uuid NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    initiating_user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX github_installation_states_expiry_idx ON github_installation_states (expires_at);

CREATE TABLE github_webhook_deliveries (
    delivery_id text PRIMARY KEY CHECK (char_length(delivery_id) BETWEEN 1 AND 128),
    event_type text NOT NULL CHECK (char_length(event_type) BETWEEN 1 AND 128),
    action text,
    github_installation_id bigint,
    status text NOT NULL CHECK (status IN ('processing', 'processed', 'ignored', 'failed')),
    error_code text,
    received_at timestamptz NOT NULL DEFAULT now(),
    processed_at timestamptz
);

CREATE INDEX github_webhook_deliveries_received_idx
    ON github_webhook_deliveries (received_at DESC);
