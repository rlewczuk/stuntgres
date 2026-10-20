package engine

import (
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

var _ plan.Runtime = (*EvalContext)(nil)

func (c *EvalContext) BeginEvaluation() error { return evaluationContext(c) }

// presentedOutput is the statement output environment with every reg* and
// aclitem formatter reading the statement's presentation snapshot.
func (c *EvalContext) presentedOutput() sqltype.OutputEnvironment {
	output := c.Output
	p := c.presentation()
	output.Regclass, output.Regtype, output.Regproc = p, p, p
	output.Regrole, output.Regcollation, output.Regnamespace = p, p, p
	output.Operator, output.ACLItem = p, p
	output.RegTS = p
	return output
}

func (c *EvalContext) ApplyCoercion(plan sqltype.CoercionPlan, value sqltype.Value, domainChecks bool) (sqltype.Value, error) {
	output := c.presentedOutput()
	input := c.Input
	if input.Regrole == nil || input.Regcollation == nil || input.Regnamespace == nil {
		if host := c.liveHost(); host != nil {
			borrowed, _ := host.Environments()
			if input.Regrole == nil {
				input.Regrole = borrowed.Regrole
			}
			if input.Regcollation == nil || input.Regnamespace == nil {
				input.Regcollation, input.Regnamespace = borrowed.Regcollation, borrowed.Regnamespace
			}
		}
	}
	if domainChecks {
		input.DomainChecks = c
	}
	return plan.Apply(c.Context, value, input, output)
}
