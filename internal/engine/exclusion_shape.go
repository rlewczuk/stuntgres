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
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Input: s.input, Output: s.output, Host: hostOf(s), Catalog: view}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1}}
	entries, err := index.ExclusionShapeEntries(&evaluation, parent, view, writes, deletes)
	if err != nil {
		return err
	}
	e := s.engine()
	e.mu.Lock()
	defer e.mu.Unlock()
	if view.Epoch() != e.catalog.state.Epoch() {
		return ids.ErrCatalogReplaced
	}
	s.beginExclusionProjectionLocked(e)
	p := e.exclusions.ProjectionLocked(s.ticket)
	p.ReplaceShape(original, parent, entries, writes, deletes)
	s.installExclusionProjectionLocked(e, p)
	return nil
}
