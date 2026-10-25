package engine

import (
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
)

// CaptureOperatorInput calls captureOperatorInput.
func CaptureOperatorInput(auth AuthorizationView, op *session.Operation) (regobj.OperatorInput, error) {
	return captureOperatorInput(auth, op)
}

// EngineRegtypeAnalysisValidLocked calls x.regtypeAnalysisValidLocked.
func EngineRegtypeAnalysisValidLocked(x *Engine, auth AuthorizationView, cell *regobj.Analysis) (bool, error) {
	return x.regtypeAnalysisValidLocked(auth, cell)
}
