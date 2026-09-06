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

export type InspectionArtifact={repository:string;commit_sha:string;regular_file_count:number;total_bytes:number;directory_count:number;maximum_depth:number;extension_distribution:Record<string,number>;manifests:string[];ecosystems:string[];ci_configuration_present:boolean;container_configuration_present:boolean;gitmodules_present:boolean;symlink_count:number;suspicious_entry_count:number;large_file_count:number;truncated:boolean;inspection_duration_ms:number;source_trust:"untrusted_repository_data"};
export type Workspace={id:string;repository_id:string;repository_full_name:string;requested_ref:string;resolved_commit_sha:string;status:"pending"|"acquiring"|"inspecting"|"succeeded"|"failed"|"cancelled"|"timed_out";inspection_artifact?:InspectionArtifact;failure_code?:string;created_at:string;started_at?:string;completed_at?:string};
