package engine

import (
	"stuntgres/internal/sqltype"
)

var _ nodeRuntime = (*EvalContext)(nil)

func (c *EvalContext) BeginEvaluation() error { return evaluationContext(c) }

func (c *EvalContext) ApplyCoercion(plan sqltype.CoercionPlan, value sqltype.Value, domainChecks bool) (sqltype.Value, error) {
	output := c.Output
	output.Regclass = c.liveScope()
	output.Regtype = c.liveScope()
	output.Regrole = c.liveScope()
	output.Regcollation = c.liveScope()
	output.Regnamespace = c.liveScope()
	output.Regproc = c.liveScope()
	output.Operator = c.liveScope()
	output.RegTS = c.liveScope()
	output.ACLItem = c.liveScope()
	input := c.Input
	if input.Regrole == nil {
		if scope := c.liveScope(); scope != nil {
			input.Regrole = scope.input.Regrole
		}
	}
	if input.Regcollation == nil || input.Regnamespace == nil {
		if scope := c.liveScope(); scope != nil {
			input.Regcollation, input.Regnamespace = scope.input.Regcollation, scope.input.Regnamespace
		}
	}
	if domainChecks {
		input.DomainChecks = c
	}
	return plan.Apply(c.Context, value, input, output)
}
