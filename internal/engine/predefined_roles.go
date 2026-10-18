package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

func (e *Engine) initializeRoles() {
	e.roles = &roleCatalog{root: authorization.Seed(&e.authorizationDomain), revision: 1}
	e.nextPrincipal = authorization.LastPredefinedRoleID + 1
}

// Return a private candidate; only successful publication advances allocation.
func (e *Engine) nextRolePrincipalLocked() (authorization.PrincipalRef, error) {
	id := e.nextPrincipal
	if id == 0 || id == ^uint64(0) {
		return authorization.PrincipalRef{}, fmt.Errorf("stuntgres: principal identity or authorization revision exhausted")
	}
	if id <= authorization.LastPredefinedRoleID {
		id = authorization.LastPredefinedRoleID + 1
	}
	ref := e.authorizationDomain.Principal(id)
	if _, exists := e.roles.root.Read(authorization.PrincipalRef{}).Find(ref); exists {
		return authorization.PrincipalRef{}, fmt.Errorf("stuntgres: principal identity already allocated")
	}
	return ref, nil
}

func predefinedPrivileges(d Descriptor, principals []authorization.PrincipalRef) authorization.Privileges {
	rights := authorization.PredefinedPrivileges(principals)
	if d.Kind() == ids.NamespaceObject {
		if rights&(authorization.SelectPrivilege|authorization.InsertPrivilege|authorization.UpdatePrivilege|authorization.DeletePrivilege) != 0 {
			return authorization.UsagePrivilege
		}
		return 0
	}
	r, ok := d.(*relationDescriptor)
	if !ok {
		return 0
	}
	if r.VirtualView || r.RelationKind == catalogRelation {
		return rights & authorization.SelectPrivilege
	}
	switch r.RelationKind {
	case sequenceRelation:
		return rights & (authorization.SelectPrivilege | authorization.UpdatePrivilege)
	case tableRelation, partitionedRelation, viewRelation, materializedViewRelation:
		return rights
	}
	return 0
}
