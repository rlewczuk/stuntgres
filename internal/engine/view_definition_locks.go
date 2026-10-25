package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/view"
)

func acquireBoundViewRelations(s *StatementScope, refs []ids.ObjectRef) error {
	return s.acquireBoundViewRelationModes(refs, nil)
}

func acquireViewDefinitionReferences(s *StatementScope, b analyze.Binding) error {
	return acquireBoundViewRelations(s, view.DefinitionReferences(b, s.LockedRef, s.QuerySources))
}
