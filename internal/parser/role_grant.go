package parser

// The GRANT/REVOKE prefix is shared until ON versus TO/FROM disambiguates
// object privileges from role names. Preserve the membership name category.
func grantMembershipRoles(items []GrantPrivilege) ([]RoleSpec, bool) {
	roles := make([]RoleSpec, len(items))
	for i, item := range items {
		if !item.RoleName || item.All || item.Columns != nil {
			return nil, false
		}
		roles[i] = RoleSpec{Name: item.Name, Pos: item.Pos}
	}
	return roles, true
}
