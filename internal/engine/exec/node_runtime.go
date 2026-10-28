package exec

import (
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

var _ plan.Runtime = (*Context)(nil)

func (c *Context) BeginEvaluation() error { return EvaluationContext(c) }

// presentedOutput is the statement output environment with every reg* and
// aclitem formatter reading the statement's presentation snapshot.
func (c *Context) presentedOutput() sqltype.OutputEnvironment {
	output := c.Output
	p := c.Presentation()
	output.Regclass, output.Regtype, output.Regproc = p, p, p
	output.Regrole, output.Regcollation, output.Regnamespace = p, p, p
	output.Operator, output.ACLItem = p, p
	output.RegTS = p
	// SQL conversions and formatting are actual output consumers. Bind only
	// this evaluation's unspecialized diagnostic owner, not literal/parameter
	// provenance or a callback retained in a cursor's output snapshot.
	if input := c.Input; input.XMLInput != nil {
		output.XMLNotice = input.XMLInput.XMLInputWarning
	}
	return output
}

func (c *Context) ApplyCoercion(plan sqltype.CoercionPlan, value sqltype.Value, domainChecks bool) (sqltype.Value, error) {
	output := c.presentedOutput()
	input := c.FunctionInput()
	if input.Regrole == nil || input.Regcollation == nil || input.Regnamespace == nil {
		if host := c.LiveHost(); host != nil {
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
