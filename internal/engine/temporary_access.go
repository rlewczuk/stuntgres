package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Statement admission serializes mutation of the sticky top-level footprint.
// Savepoint restoration deliberately never restores it.
func (e *Engine) markTemporaryAccessLocked(scope *StatementScope, ref ids.ObjectRef) {
	catalog := e.privateCatalogLocked(scope.owner.Catalog)
	if scope.catalog != nil {
		catalog = e.privateCatalogLocked(scope.catalog)
	}
	d, ok := catalog.Relation(ref.Address)
	if !ok {
		return
	}
	if ns, ok := catalog.Namespace(d.Namespace()); ok && ns.Temporary == scope.op.Session.SessionToken {
		scope.owner.AccessedTemporary = true
	}
}

func temporaryDataTarget(auth AuthorizationView, b *analyze.Binding, stmt parser.Statement) bool {
	switch stmt.(type) {
	case *parser.Select:
		if !parser.HasModifyingCTE(stmt) {
			return false
		}
		for _, definition := range b.CTEs {
			if mutation, ok := analyze.MutationBinding(definition); ok && !temporaryDataTarget(auth, mutation, definition.Statement) {
				return false
			}
		}
		return true
	case *parser.Copy, *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
		if b.Relation != nil {
			ns, ok := auth.Catalog.Namespace(b.Relation.Namespace())
			return ok && ownTemporaryNamespace(auth, ns)
		}
	}
	return false
}
