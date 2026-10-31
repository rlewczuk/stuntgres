package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// The statistics import runtime's host is the statement host; AuthorizationView
// is its authority. Operation, Context, Environments, PendingCatalog,
// EngineStorageLocked and the Engine.Mu pair are shared with exec.Host and the
// EXPLAIN renderer, RoutineAuthorizationLocked with the contrib runtimes.

func (h *statementHost) ResolveRelationLocked(auth AuthorizationView, name parser.ObjectName) (ids.ObjectRef, error) {
	return h.scope().Engine().ResolveRelationReference(auth, name, true)
}

func (h *statementHost) VacuumAllowedLocked(auth AuthorizationView, relation *catalog.Relation) (bool, error) {
	return table.VacuumAllowed(h.scope().executionContext(), auth.View, auth, relation)
}

// AcquireObjectLock takes a statement lock on ref with nothing held, reserving
// the replacement read like a sequence acquisition.
func (h *statementHost) AcquireObjectLock(ref ids.ObjectRef, mode lock.Mode) error {
	return h.scope().AcquireSequence(ref, mode, true)
}

func (h *statementHost) PublishedRelationLocked(address ids.ObjectAddress) (*catalog.Relation, bool) {
	return h.scope().Engine().Catalog.State.Relation(address)
}

// StatementCatalogLocked is the statement's private catalog journal, cloned
// from the pending one on its first write.
func (h *statementHost) StatementCatalogLocked() *session.Journal {
	s := h.scope()
	if s.Catalog == nil {
		s.Catalog = s.PendingCatalog().Clone()
	}
	return s.Catalog
}
