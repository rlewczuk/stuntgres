package engine

import "stuntgres/internal/engine/catalog"

// view is the read the retired constraints are named against: the stage's
// base while staging, the definition base while preparing.
// retiredBase is the read inheritRetiredColumnConstraints names against.
func (state *InheritanceAlterState) retiredBase() catalog.Read {
	if state.Stage != nil {
		return state.Stage.Base
	}
	return state.Definition.Base
}
