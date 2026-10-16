package authorization

import (
	"fmt"
	"strings"
	"stuntgres/internal/parser"
)

func ObjectPrivilege(item parser.GrantPrivilege, kind string) (Privileges, error) {
	classMask := TableGrantPrivileges
	switch kind {
	case "sequence":
		classMask = DefaultMask(DefaultSequences)
	case "schema":
		classMask = DefaultMask(DefaultSchemas)
	case "type", "domain":
		classMask = DefaultMask(DefaultTypes)
	case "function", "procedure", "routine":
		classMask = DefaultMask(DefaultRoutines)
	case "database":
		classMask = CreatePrivilege | TemporaryPrivilege | ConnectPrivilege
	case "parameter":
		classMask = DefaultMask(DefaultParameters)
	case "tablespace":
		classMask = DefaultMask(DefaultTablespaces)
	case "language", "foreign-data wrapper", "foreign server":
		classMask = UsagePrivilege
	case "large object":
		classMask = SelectPrivilege | UpdatePrivilege
	case "relation":
		// An implicit/TABLE target can resolve to a sequence.
		classMask |= DefaultMask(DefaultSequences)
	}
	if kind != "" && kind != "relation" && item.Columns != nil {
		return 0, sqlError("0LP01", "column privileges are only valid for relations")
	}
	if item.All {
		if item.Columns != nil {
			return ColumnGrantPrivileges, nil
		}
		return classMask, nil
	}
	mask, err := grantPrivilegeMask(item.Name)
	if err != nil {
		return 0, err
	}
	privilegeName := strings.ToUpper(item.Name)
	if mask == TemporaryPrivilege {
		privilegeName = "TEMP"
	}
	if mask == 0 || mask&^classMask != 0 {
		noun := "relation"
		if kind != "" {
			noun = kind
		}
		return 0, sqlError("0LP01", "invalid privilege type "+privilegeName+" for "+noun)
	}
	if item.Columns != nil && mask&^ColumnGrantPrivileges != 0 {
		return 0, sqlError("0LP01", "invalid privilege type "+privilegeName+" for column")
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
	case "connect":
		return ConnectPrivilege, nil
	case "temporary", "temp":
		return TemporaryPrivilege, nil
	case "set":
		return SetPrivilege, nil
	case "alter system":
		return AlterSystemPrivilege, nil
	default:
		return 0, sqlError("42601", fmt.Sprintf(`unrecognized privilege type "%s"`, name))
	}
}
