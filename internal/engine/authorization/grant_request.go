package authorization

import (
	"fmt"
	"strings"
	"stuntgres/internal/parser"
)

func ObjectPrivilege(item parser.GrantPrivilege) (Privileges, error) {
	if item.All {
		if item.Columns != nil {
			return ColumnGrantPrivileges, nil
		}
		return TableGrantPrivileges, nil
	}
	var mask Privileges
	switch strings.ToLower(item.Name) {
	case "select":
		mask = SelectPrivilege
	case "insert":
		mask = InsertPrivilege
	case "update":
		mask = UpdatePrivilege
	case "delete":
		mask = DeletePrivilege
	case "truncate":
		mask = TruncatePrivilege
	case "references":
		mask = ReferencesPrivilege
	case "trigger":
		mask = TriggerPrivilege
	case "maintain":
		mask = MaintainPrivilege
	case "create", "usage", "execute", "connect", "temporary", "temp", "set", "alter system":
		return 0, sqlError("0LP01", "invalid privilege type "+strings.ToUpper(item.Name)+" for relation")
	default:
		return 0, sqlError("42601", fmt.Sprintf(`unrecognized privilege type "%s"`, item.Name))
	}
	if item.Columns != nil && mask&^ColumnGrantPrivileges != 0 {
		return 0, sqlError("0LP01", "invalid privilege type "+strings.ToUpper(item.Name)+" for column")
	}
	return mask, nil
}
