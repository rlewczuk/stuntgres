package core

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

type temporaryAccessMode uint8

const (
	temporaryOwnAccess temporaryAccessMode = iota
	temporaryTargetAttempt
)

// Statement admission serializes mutation of the sticky top-level footprint.
// Savepoint restoration deliberately never restores it. Ordinary callers record
// only their own TEMP access; admitted runtime targets can record an attempted
// foreign TEMP access before storage admission rejects it.
func (e *Engine) markTemporaryAccessLocked(scope *StatementScope, ref ids.ObjectRef, mode temporaryAccessMode) {
	catalog := e.PrivateCatalogLocked(scope.Owner.Catalog)
	if scope.Catalog != nil {
		catalog = e.PrivateCatalogLocked(scope.Catalog)
	}
	d, ok := catalog.Relation(ref.Address)
	if !ok {
		return
	}
	if ns, ok := catalog.Namespace(d.Namespace()); ok && ns.Temporary != (ids.SessionToken{}) && (mode == temporaryTargetAttempt || ns.Temporary == scope.Op.Session.SessionToken) {
		scope.Owner.AccessedTemporary = true
	}
}

func TemporaryDataTarget(auth AuthorizationView, b *analyze.Binding, stmt parser.Statement) bool {
	switch stmt.(type) {
	case *parser.Select:
		if !parser.HasModifyingCTE(stmt) {
			return false
		}
		for _, definition := range b.CTEs {
			if mutation, ok := analyze.MutationBinding(definition); ok && !TemporaryDataTarget(auth, mutation, definition.Statement) {
				return false
			}
		}
		return true
	case *parser.Copy, *parser.Insert, *parser.Update, *parser.Delete, *parser.Merge:
		if b.Relation != nil {
			ns, ok := auth.Catalog.Namespace(b.Relation.Namespace())
			return ok && ns.Temporary != (ids.SessionToken{}) && access.OwnTemporaryNamespace(temporaryNamespace(auth), ns)
		}
	}
	return false
}
