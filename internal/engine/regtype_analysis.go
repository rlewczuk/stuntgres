package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/sequence"
)

// regtypeObservationLocked is what prepared reg* literals are analyzed and
// validated against, with auth's type and collation lookup paths. Caller
// holds Engine.mu; the path callbacks run under it.
func (e *Engine) regtypeObservationLocked(auth AuthorizationView) (regobj.Observation, regobj.LookupPaths) {
	return regobj.Observation{Domain: e.catalog.Domain, Database: auth.session.DatabaseName, View: auth.View, Environment: auth},
		regobj.LookupPaths{
			Type:      func() (sequence.Environment, error) { return e.sequenceEnvironmentLocked(auth) },
			Collation: func() ([]ids.ObjectAddress, error) { return lookupPath(auth, analyze.CollationLookup) },
		}
}

// regtypeAnalysisLocked captures a prepared statement's reg* literals. A
// statement without them builds no observation.
func (e *Engine) regtypeAnalysisLocked(auth AuthorizationView, constants []analyze.RegtypeConstant) (*regobj.Analysis, error) {
	if len(constants) == 0 {
		return nil, nil
	}
	o, paths := e.regtypeObservationLocked(auth)
	return regobj.NewAnalysis(o, paths, constants)
}

// regtypeAnalysisValidLocked validates a held cell; a missing cell is valid
// without an observation.
func (e *Engine) regtypeAnalysisValidLocked(auth AuthorizationView, cell *regobj.Analysis) (bool, error) {
	if cell == nil {
		return true, nil
	}
	o, paths := e.regtypeObservationLocked(auth)
	return cell.Valid(o, paths)
}
