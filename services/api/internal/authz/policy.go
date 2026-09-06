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
)

var grants = map[Role]map[Permission]bool{
	RoleOwner:  {OrganizationRead: true, OrganizationManage: true, MembersRead: true, MembersManage: true, GitHubRead: true, GitHubManage: true, RepositoriesRead: true, WorkspacesCreate: true, WorkspacesRead: true, WorkspacesCancel: true},
	RoleAdmin:  {OrganizationRead: true, OrganizationManage: true, MembersRead: true, MembersManage: true, GitHubRead: true, GitHubManage: true, RepositoriesRead: true, WorkspacesCreate: true, WorkspacesRead: true, WorkspacesCancel: true},
	RoleMember: {OrganizationRead: true, MembersRead: true, GitHubRead: true, RepositoriesRead: true, WorkspacesCreate: true, WorkspacesRead: true, WorkspacesCancel: true},
}

func Allowed(role Role, permission Permission) bool {
	return grants[role][permission]
}
