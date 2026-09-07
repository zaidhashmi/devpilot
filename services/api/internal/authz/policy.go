package authz

type Role string
type Permission string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"

	OrganizationRead   Permission = "organization.read"
	OrganizationManage Permission = "organization.manage"
	MembersRead        Permission = "members.read"
	MembersManage      Permission = "members.manage"
	GitHubRead         Permission = "github.read"
	GitHubManage       Permission = "github.manage"
	RepositoriesRead   Permission = "repositories.read"
	WorkspacesCreate   Permission = "workspaces.create"
	WorkspacesRead     Permission = "workspaces.read"
	WorkspacesCancel   Permission = "workspaces.cancel"
	TasksCreate        Permission = "tasks.create"
	TasksRead          Permission = "tasks.read"
	TasksCancel        Permission = "tasks.cancel"
	TaskRunsCreate     Permission = "task_runs.create"
	TaskRunsRead       Permission = "task_runs.read"
	TaskRunsCancel     Permission = "task_runs.cancel"
	ApprovalsRead      Permission = "approvals.read"
	ApprovalsDecide    Permission = "approvals.decide"
)

var grants = map[Role]map[Permission]bool{
	RoleOwner:  {OrganizationRead: true, OrganizationManage: true, MembersRead: true, MembersManage: true, GitHubRead: true, GitHubManage: true, RepositoriesRead: true, WorkspacesCreate: true, WorkspacesRead: true, WorkspacesCancel: true, TasksCreate: true, TasksRead: true, TasksCancel: true, TaskRunsCreate: true, TaskRunsRead: true, TaskRunsCancel: true, ApprovalsRead: true, ApprovalsDecide: true},
	RoleAdmin:  {OrganizationRead: true, OrganizationManage: true, MembersRead: true, MembersManage: true, GitHubRead: true, GitHubManage: true, RepositoriesRead: true, WorkspacesCreate: true, WorkspacesRead: true, WorkspacesCancel: true, TasksCreate: true, TasksRead: true, TasksCancel: true, TaskRunsCreate: true, TaskRunsRead: true, TaskRunsCancel: true, ApprovalsRead: true, ApprovalsDecide: true},
	RoleMember: {OrganizationRead: true, MembersRead: true, GitHubRead: true, RepositoriesRead: true, WorkspacesCreate: true, WorkspacesRead: true, WorkspacesCancel: true, TasksCreate: true, TasksRead: true, TaskRunsCreate: true, TaskRunsRead: true, TaskRunsCancel: true, ApprovalsRead: true, ApprovalsDecide: true},
}

func Allowed(role Role, permission Permission) bool {
	return grants[role][permission]
}
