package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func PredefinedPrivileges(d catalog.Descriptor, principals []authorization.PrincipalRef) authorization.Privileges {
	rights := authorization.PredefinedPrivileges(principals)
	if d.Kind() == ids.NamespaceObject {
		if rights&(authorization.SelectPrivilege|authorization.InsertPrivilege|authorization.UpdatePrivilege|authorization.DeletePrivilege) != 0 {
			return authorization.UsagePrivilege
		}
		return 0
	}
	r, ok := d.(*catalog.Relation)
	if !ok {
		return 0
	}
	if r.VirtualView || r.RelationKind == catalog.CatalogRelation {
		return rights & authorization.SelectPrivilege
	}
	switch r.RelationKind {
	case catalog.SequenceRelation:
		return rights & (authorization.SelectPrivilege | authorization.UpdatePrivilege)
	case catalog.TableRelation, catalog.PartitionedRelation, catalog.ViewRelation, catalog.MaterializedViewRelation:
		return rights
	}
	return 0
}
