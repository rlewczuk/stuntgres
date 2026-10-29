package role

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// DROP OWNED invokes an address-less REVOKE for each actual shared ACL role
// dependency. Parent and column dependencies are independent, including repeated
// table-wide revokes; owner, policy and default-ACL dependencies are not grants.
func recordOwnedACLCommands(stage OwnedCleanupStage, descriptor catalog.Descriptor, sources map[authorization.PrincipalRef]bool) {
	collector, ok := stage.(interface {
		CollectsDDLCommands() bool
		RecordDDLPrivileges(string, string)
	})
	if !ok || !collector.CollectsDDLCommands() {
		return
	}
	kind := ""
	switch descriptor.Address().ClassID {
	case ids.RelationClassID:
		kind = "TABLE"
	case ids.ProcedureClassID:
		kind = "ROUTINE"
	case ids.TypeClassID:
		kind = "TYPE"
	case ids.NamespaceClassID:
		kind = "SCHEMA"
	case ids.LanguageClassID:
		kind = "LANGUAGE"
	case ids.LargeObjectClassID:
		kind = "LARGE OBJECT"
	case ids.ForeignWrapperClassID:
		kind = "FOREIGN DATA WRAPPER"
	case ids.ForeignServerClassID:
		kind = "FOREIGN SERVER"
	}
	if kind == "" {
		return
	}
	owner, acl, columns := access.DescriptorACL(descriptor)
	for role := range sources {
		if role == owner {
			continue
		}
		record := func(value authorization.ACLValue) {
			for _, entry := range value.Entries {
				if entry.Grantor == role || !entry.Grantee.Public && entry.Grantee.Role == role {
					collector.RecordDDLPrivileges("REVOKE", kind)
					return
				}
			}
		}
		record(acl)
		for _, column := range columns {
			record(column)
		}
	}
}
