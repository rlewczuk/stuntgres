package viewcmds

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/view"
)

func acquireBoundViewRelations(s *core.StatementScope, refs []ids.ObjectRef) error {
	return s.AcquireBoundViewRelationModes(refs, nil)
}

func acquireViewDefinitionReferences(s *core.StatementScope, b analyze.Binding) error {
	return acquireBoundViewRelations(s, view.DefinitionReferences(b, s.LockedRef, s.QuerySources))
}
