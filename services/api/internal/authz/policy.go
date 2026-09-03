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
)

var grants = map[Role]map[Permission]bool{
	RoleOwner:  {OrganizationRead: true, OrganizationManage: true, MembersRead: true, MembersManage: true},
	RoleAdmin:  {OrganizationRead: true, OrganizationManage: true, MembersRead: true, MembersManage: true},
	RoleMember: {OrganizationRead: true, MembersRead: true},
}

func Allowed(role Role, permission Permission) bool {
	return grants[role][permission]
}
