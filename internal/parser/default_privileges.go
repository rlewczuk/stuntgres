package parser

// Options remain ordered so execution can diagnose the second redundant clause.
type DefaultPrivilegesOption struct {
	Pos     Position
	Roles   []RoleSpec
	Schemas []Identifier
}

type AlterDefaultPrivileges struct {
	Options     []DefaultPrivilegesOption
	Class       string
	Privileges  []GrantPrivilege
	Grantees    []GrantGrantee
	GrantOption bool
	Revoke      bool
	OptionOnly  bool
	Cascade     bool
}

func (*AlterDefaultPrivileges) statement() {}
