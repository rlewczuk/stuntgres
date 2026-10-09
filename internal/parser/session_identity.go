package parser

import "strings"

func IdentityFunctionName(name string) bool {
	switch name {
	case "current_catalog", "current_database", "current_role", "current_user", "session_user", "user", "system_user", "current_schema":
		return true
	}
	// Quoted uppercase names must reach signature resolution without folding the
	// retained identifier. EqualFold does not allocate a normalized spelling.
	switch len(name) {
	case 4:
		return strings.EqualFold(name, "user")
	case 11:
		return strings.EqualFold(name, "system_user")
	case 12:
		return strings.EqualFold(name, "current_role") || strings.EqualFold(name, "current_user") || strings.EqualFold(name, "session_user")
	case 14:
		return strings.EqualFold(name, "current_schema")
	case 15:
		return strings.EqualFold(name, "current_catalog")
	case 16:
		return strings.EqualFold(name, "current_database")
	}
	return false
}
