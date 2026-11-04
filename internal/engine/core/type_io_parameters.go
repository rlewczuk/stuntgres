package core

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

// borrowParameterInput belongs to one synchronous actual utility/wire importer,
// not to its retained statement or settings. Planned query imports reuse their
// existing evaluator instead. Diagnostic parameter rendering is not a second
// SQL output-function call and deliberately keeps its ordinary output adapter.
func (s *StatementScope) borrowParameterInput(types []sqltype.Usage) (sqltype.InputEnvironment, error) {
	input := s.Input
	e := s.Engine()
	if e.Sepgsql == nil {
		return input, nil
	}
	needed := input.ByteTransport && input.Transport.ID() != 0 && input.Transport.ID() != 6
	for _, usage := range types {
		if sqltype.ContainsInstalledScalarIO(usage.Type) {
			needed = true
			break
		}
	}
	if !needed {
		return input, nil
	}
	e.Mu.Lock()
	auth, err := s.RoutineAuthorizationLocked()
	e.Mu.Unlock()
	if err != nil {
		return sqltype.InputEnvironment{}, err
	}
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: s.executionContext(), Host: HostOf(s), Catalog: auth.Catalog, Input: input, Output: s.Output}}
	input.TypeIO, input.Conversion = evaluation, evaluation
	return input, nil
}
