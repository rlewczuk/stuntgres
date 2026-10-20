package engine

import "stuntgres/internal/engine/ids"

// Statement admission serializes mutation of the sticky top-level footprint.
// Savepoint restoration deliberately never restores it.
func (e *Engine) markTemporaryAccessLocked(scope *StatementScope, ref ids.ObjectRef) {
	catalog := e.privateCatalogLocked(scope.owner.catalog)
	if scope.catalog != nil {
		catalog = e.privateCatalogLocked(scope.catalog)
	}
	d, ok := catalog.Relation(ref.Address)
	if !ok {
		return
	}
	if ns, ok := catalog.Namespace(d.Namespace()); ok && ns.Temporary == scope.op.Session.token {
		scope.owner.accessedTemporary = true
	}
}
