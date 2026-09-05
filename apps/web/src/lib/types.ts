export type Actor = {
  user: { id: string; email: string; display_name: string; status: string };
  organization: { id: string; name: string; slug: string; status: string };
  membership: { id: string; organization_id: string; user_id: string; role: "owner" | "admin" | "member"; status: string };
};

export type GitHubInstallation = {
  id: string; github_installation_id: number; github_account_id: number;
  github_account_login: string; github_account_type: string;
  repository_selection: "all" | "selected"; status: "active" | "suspended" | "removed";
  repository_count: number; suspended_at?: string;
};

export type Repository = {
  id: string; github_repository_id: number; owner: string; name: string; full_name: string;
  default_branch: string; private: boolean; archived: boolean; disabled: boolean;
  available: boolean; html_url: string; last_synced_at: string;
};
