package core

import (
	"stuntgres/internal/engine/sequence"
)

// EngineSequenceEnvironmentLocked calls x.sequenceEnvironmentLocked.
func EngineSequenceEnvironmentLocked(x *Engine, auth AuthorizationView) (sequence.Environment, error) {
	return x.sequenceEnvironmentLocked(auth)
}

// EngineSequenceAnalysisValidLocked calls x.sequenceAnalysisValidLocked.
func EngineSequenceAnalysisValidLocked(x *Engine, auth AuthorizationView, analysis *sequence.Analysis) (bool, error) {
	return x.sequenceAnalysisValidLocked(auth, analysis)
}
