package visibility

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// HeapOperations is the existing ordinary heap's concrete visibility authority.
// It has no state: callers retain the actual storage roots and snapshot frontier.
// Relation selection binds it through the exact captured method/handler identity.
type HeapOperations struct{}

// ResolveTableAccess resolves the finite native table implementation, not a
// PostgreSQL ABI pointer or an extensible callback registry.
func ResolveTableAccess(native catalog.NativeAccessMethod) (HeapOperations, bool) {
	return HeapOperations{}, native == catalog.HeapAccessMethod
}

// ResolveHeapAccess binds once from a captured catalog. A signature or enum
// marker alone cannot grant access: the real method and installed handler must
// satisfy the same genuine provider contract as method publication.
func ResolveHeapAccess(view catalog.Read, methodOID ids.OID) (HeapOperations, error) {
	method, ok := view.AccessMethod(methodOID)
	if !ok {
		return HeapOperations{}, fmt.Errorf("stuntgres: unavailable heap access method")
	}
	operations, ok := ResolveTableAccess(method.Native)
	if !ok || method.Handler != 3 {
		return HeapOperations{}, fmt.Errorf("stuntgres: unavailable heap access method")
	}
	if err := (catalog.AccessMethodProvider{}).Validate(view, method); err != nil {
		return HeapOperations{}, err
	}
	return operations, nil
}

// ResolveRelationHeapAccess never reads a live descriptor beside a captured
// source: identity, method and handler all belong to this same catalog epoch.
func ResolveRelationHeapAccess(view catalog.Read, ref ids.ObjectRef) (HeapOperations, error) {
	if view.Owner() == nil || ref.Domain() != view.Owner().Domain || ref.Epoch != view.Epoch() {
		return HeapOperations{}, ids.ErrCatalogReplaced
	}
	relation, ok := view.Relation(ref.Address)
	if !ok || !relation.RelationKind.Stored() { return HeapOperations{}, fmt.Errorf("stuntgres: invalid physical table access target") }
	return ResolveHeapAccess(view, relation.TableMethod)
}
