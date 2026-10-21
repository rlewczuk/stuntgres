package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
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

// Namespace lookup and role privileges precede physical storage admission.
// Identity/metadata providers do not acquire another session's stored TEMP data.
func checkTemporaryStorageAccess(catalog catalog.Read, relation *catalog.Relation, token ids.SessionToken) error {
	if relation == nil {
		return ids.ErrCatalogReplaced
	}
	if !relation.RelationKind.Stored() {
		return nil
	}
	namespace, ok := catalog.Namespace(relation.Namespace())
	if !ok {
		return ids.ErrCatalogReplaced
	}
	if namespace.Temporary != (ids.SessionToken{}) && namespace.Temporary != token {
		return sqlError("0A000", "cannot access temporary tables of other sessions")
	}
	return nil
}
