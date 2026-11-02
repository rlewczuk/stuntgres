package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// The router has already admitted and locked this exact leaf. Capturing only a
// reached destination keeps unused foreign partitions free of mapping/handler
// requirements. Application callbacks run later, outside both engine locks.
func (h *statementHost) CaptureForeignPartition(ref ids.ObjectRef) (*exec.ForeignQuerySource, error) {
	s := h.scope()
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	state := s.PartitionInsert
	if state == nil || ref.Domain() != e.Catalog.Domain || ref.Epoch != state.Catalog.Epoch() {
		return nil, ids.ErrCatalogReplaced
	}
	relation, ok := state.Catalog.Relation(ref.Address)
	if !ok || relation.RelationKind != catalog.ForeignRelation || e.PrivateCatalogLocked(s.PendingCatalog()).Get(ref.Address) != relation {
		return nil, ids.ErrCatalogReplaced
	}
	auth, err := e.AuthorizationLocked(s.Op)
	if err != nil {
		return nil, err
	}
	source, err := e.captureForeignSourceLocked(s, ref, relation, auth.State.CurrentUser, nil, plan.RowMark{})
	if err != nil {
		return nil, err
	}
	return source.Foreign, nil
}
