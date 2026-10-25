package core

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
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

// InputCapture calls inputCapture.
func InputCapture(auth AuthorizationView, path []ids.ObjectAddress) regobj.InputCapture {
	return inputCapture(auth, path)
}

// EngineCaptureRegclassOutput calls x.captureRegclassOutput.
func EngineCaptureRegclassOutput(x *Engine, op *session.Operation, rows *exec.FinalRows) error {
	return x.captureRegclassOutput(op, rows)
}
