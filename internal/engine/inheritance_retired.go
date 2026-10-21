package engine

import "stuntgres/internal/engine/catalog"

// view is the read the retired constraints are named against: the stage's
// base while staging, the definition base while preparing.
// retiredBase is the read inheritRetiredColumnConstraints names against.
func (state *inheritanceAlterState) retiredBase() catalog.Read {
	if state.stage != nil {
		return state.stage.Base
	}
	return state.Definition.Base
}
