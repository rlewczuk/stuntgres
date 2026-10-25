package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
)

func (s *StatementScope) prepareExclusionShape(original, parent *catalog.Relation, view catalog.Read, writes []storage.RowVersion, deletes *visibility.PrivateDeletes) error {
	if len(original.Constraints.Exclusion)+len(parent.Constraints.Exclusion) == 0 {
		return nil
	}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.Op.Context(), Input: s.Input, Output: s.Output, Host: HostOf(s), Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	entries, err := index.ExclusionShapeEntries(&evaluation, parent, view, writes, deletes)
	if err != nil {
		return err
	}
	e := s.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if view.Epoch() != e.Catalog.State.Epoch() {
		return ids.ErrCatalogReplaced
	}
	s.BeginExclusionProjectionLocked(e)
	p := e.Exclusions.ProjectionLocked(s.LockTicket)
	p.ReplaceShape(original, parent, entries, writes, deletes)
	s.InstallExclusionProjectionLocked(e, p)
	return nil
}
