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
export type EngineeringTask={id:string;repository_id:string;repository_full_name:string;created_by_user_id:string;title:string;objective:string;status:"active"|"completed"|"cancelled"|"failed";created_at:string;updated_at:string;closed_at?:string};
export type PlanRevision={id:string;task_run_id:string;revision_number:number;source:"deterministic";summary:string;structured_payload:{objective:string;repository:string;commit_sha:string;detected_ecosystems?:string[];manifests?:string[];inspection_summary?:Record<string,number>;recommended_next_stage:string;source_trust:string};created_at:string};
export type Approval={id:string;task_run_id:string;plan_revision_id:string;approval_type:"proceed_to_analysis";revision:number;status:"pending"|"approved"|"rejected"|"cancelled"|"superseded";requested_at:string;decided_at?:string;decided_by_user_id?:string;decision_comment?:string};
export type TaskRun={id:string;engineering_task_id:string;repository_id:string;repository_full_name:string;workspace_id?:string;run_number:number;requested_ref:string;resolved_commit_sha:string;status:"pending"|"inspecting"|"awaiting_approval"|"approved"|"rejected"|"cancelled"|"failed"|"timed_out"|"completed";current_stage:"repository_snapshot"|"repository_inspection"|"planning"|"approval"|"finished";created_by_user_id:string;failure_code?:string;cancellation_requested_at?:string;created_at:string;started_at?:string;completed_at?:string;workspace?:Workspace;plan_revision?:PlanRevision;approval?:Approval};
