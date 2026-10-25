package engine

import "stuntgres/internal/engine/core"

import "stuntgres/internal/engine/catalog"

// view is the read the retired constraints are named against: the stage's
// base while staging, the definition base while preparing.
// retiredBase is the read inheritRetiredColumnConstraints names against.
func retiredBase(state *core.InheritanceAlterState) catalog.Read {
	if state.Stage != nil {
		return state.Stage.Base
	}
	return state.Definition.Base
}
