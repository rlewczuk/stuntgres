package schemacmds

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
)

// Membership captures raw ACLs once, independently of the installation recipe.
// Reciprocal companions without a stored ACL do not acquire synthetic history.
func recordExtensionMemberPrivileges(stage *core.CatalogStage, view catalog.Read, descriptor catalog.Descriptor) error {
	address := descriptor.Address()
	if _, err := catalog.InitialPrivilegeMask(view, address); err != nil {
		return nil // This supported member class has no initial-ACL catalog representation.
	}
	acl, err := access.RawObjectACL(view, address)
	if err != nil {
		return err
	}
	if acl.Explicit && len(acl.Entries) != 0 {
		stage.RecordInitialPrivilege(address, 'e', acl)
	}
	if relation, ok := descriptor.(*catalog.Relation); ok {
		for sub, acl := range relation.Security.ColumnACL {
			if sub > 0 && relation.HasAttribute(sub) && acl.Explicit && len(acl.Entries) != 0 {
				column := address
				column.SubID = sub
				stage.RecordInitialPrivilege(column, 'e', acl)
			}
		}
	}
	return nil
}
