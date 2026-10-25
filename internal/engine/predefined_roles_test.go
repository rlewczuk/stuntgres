package engine

import (
	"stuntgres/internal/parser"
)

func predefinedMembership(target, member string) *parser.RoleMembership {
	return &parser.RoleMembership{Targets: []parser.RoleSpec{{Name: target}}, Members: []parser.RoleSpec{{Name: member}}}
}
