package engine

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

// Both SQL cursors and protocol producers retain the admitted policy graph and
// nested-source pins. Ownership moves only at successful producer publication;
// ordinary/error paths leave the statement's cleanup owner unchanged.
func (s *StatementScope) takeRowSecurityPrograms(c *sqlCursor) {
	c.rlsPrograms, s.rlsPrograms = s.rlsPrograms, nil
	environment := func(input *sqltype.InputEnvironment, output *sqltype.OutputEnvironment) {
		input.LockedDomainInput, input.DomainChecks = nil, nil
		regobj.DetachCursorInput(input, c.authority)
		output.Regclass, output.Regtype = nil, nil
		output.Regproc, output.Regrole, output.Operator = nil, nil, nil
		output.Regcollation, output.Regnamespace, output.RegTS = nil, nil, nil
	}
	detach := func(clause *exec.RLSRuntimeClause) {
		local := &clause.Evaluation
		local.Host, local.Context = nil, nil
		local.EvalFrame = exec.EvalFrame{}
		local.RoutineAuthority = nil
		local.PreparedChecks, local.PreparedDomainChecks, local.PreparedGenerations = nil, nil, nil
		environment(&local.Input, &local.Output)
		clause.State.Input = local.Input
		clause.State.Output = local.Output
		exec.DetachRowSecurityState(&clause.State, c.authority, environment)
	}
	for _, program := range c.rlsPrograms {
		for i := range program.Groups {
			for j := range program.Groups[i].Permissive {
				detach(&program.Groups[i].Permissive[j])
			}
			for j := range program.Groups[i].Restrictive {
				detach(&program.Groups[i].Restrictive[j])
			}
		}
	}
}
