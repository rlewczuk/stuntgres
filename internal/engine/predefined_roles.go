package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func (e *Engine) initializeRoles() {
	e.roles.published = &roleCatalog{root: authorization.Seed(&e.roles.domain), revision: 1}
	e.roles.nextPrincipal = authorization.LastPredefinedRoleID + 1
}

// Return a private candidate; only successful publication advances allocation.
func (e *Engine) nextRolePrincipalLocked() (authorization.PrincipalRef, error) {
	id := e.roles.nextPrincipal
	if id == 0 || id == ^uint64(0) {
		return authorization.PrincipalRef{}, fmt.Errorf("stuntgres: principal identity or authorization revision exhausted")
	}
	if id <= authorization.LastPredefinedRoleID {
		id = authorization.LastPredefinedRoleID + 1
	}
	ref := e.roles.domain.Principal(id)
	if _, exists := e.roles.published.root.Read(authorization.PrincipalRef{}).Find(ref); exists {
		return authorization.PrincipalRef{}, fmt.Errorf("stuntgres: principal identity already allocated")
	}
	return ref, nil
}

func predefinedPrivileges(d catalog.Descriptor, principals []authorization.PrincipalRef) authorization.Privileges {
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
