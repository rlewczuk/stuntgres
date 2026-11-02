package core

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// CheckGINPendingListTargetLocked uses current transaction/private descriptors;
// routine EXECUTE admission remains with the ordinary expression caller.
func (h *statementHost) CheckGINPendingListTargetLocked(oid uint32) error {
	s := h.scope()
	view := h.PrivateCatalogLocked(h.PendingCatalog())
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}
	target, found := view.Relation(address)
	if !found {
		return SQLError("XX000", fmt.Sprintf("could not open relation with OID %d", oid))
	}
	if target.RelationKind != catalog.IndexRelation || target.SQLIndex == nil {
		return SQLError("42809", fmt.Sprintf("\"%s\" is not an index", target.Name()))
	}
	method, found := view.AccessMethod(target.SQLIndex.MethodIdentity())
	if !found {
		return ids.ErrCatalogReplaced
	}
	implementation, supported := method.IndexImplementation()
	if !supported || implementation != catalog.GinMethod {
		return SQLError("42809", fmt.Sprintf("\"%s\" is not a GIN index", target.Name()))
	}
	if namespace, found := view.Namespace(target.Namespace()); found && namespace.Temporary != (ids.SessionToken{}) && namespace.Temporary != s.Op.Session.SessionToken {
		return SQLError("0A000", "cannot access temporary indexes of other sessions")
	}
	auth, err := s.RoutineAuthorizationLocked()
	if err != nil {
		return err
	}
	auth.Catalog = view
	auth.Recapture(view)
	return auth.Check(access.Requirement{Object: auth.Ref(address), OwnerOnly: true, OwnerNoun: "index"})
}
