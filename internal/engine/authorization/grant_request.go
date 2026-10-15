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
	mask, err := grantPrivilegeMask(item.Name)
	if err != nil {
		return 0, err
	}
	if mask == 0 || mask & ^TableGrantPrivileges != 0 {
		return 0, sqlError("0LP01", "invalid privilege type "+strings.ToUpper(item.Name)+" for relation")
	}
	if item.Columns != nil && mask&^ColumnGrantPrivileges != 0 {
		return 0, sqlError("0LP01", "invalid privilege type "+strings.ToUpper(item.Name)+" for column")
	}
	return mask, nil
}

// ForeignPrivilege validates both GRANT and REVOKE using the shared recognized
// privilege vocabulary, but the SQL/MED object's USAGE-only mask and diagnostics.
func ForeignPrivilege(item parser.GrantPrivilege, kind string) (Privileges, error) {
	if item.Columns != nil {
		return 0, sqlError("0LP01", "column privileges are only valid for relations")
	}
	if item.All {
		return UsagePrivilege, nil
	}
	mask, err := grantPrivilegeMask(item.Name)
	if err != nil {
		return 0, err
	}
	if mask != UsagePrivilege {
		name := strings.ToUpper(item.Name)
		if name == "TEMPORARY" {
			name = "TEMP"
		}
		return 0, sqlError("0LP01", "invalid privilege type "+name+" for "+kind)
	}
	return mask, nil
}

func grantPrivilegeMask(name string) (Privileges, error) {
	switch strings.ToLower(name) {
	case "select":
		return SelectPrivilege, nil
	case "insert":
		return InsertPrivilege, nil
	case "update":
		return UpdatePrivilege, nil
	case "delete":
		return DeletePrivilege, nil
	case "truncate":
		return TruncatePrivilege, nil
	case "references":
		return ReferencesPrivilege, nil
	case "trigger":
		return TriggerPrivilege, nil
	case "maintain":
		return MaintainPrivilege, nil
	case "usage":
		return UsagePrivilege, nil
	case "create":
		return CreatePrivilege, nil
	case "execute":
		return ExecutePrivilege, nil
	case "connect", "temporary", "temp", "set", "alter system":
		return 0, nil // Recognized privileges without a table/SQL-MED ACL bit.
	default:
		return 0, sqlError("42601", fmt.Sprintf(`unrecognized privilege type "%s"`, name))
	}
}
