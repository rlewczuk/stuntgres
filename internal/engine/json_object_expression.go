package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Explicit VARIADIC already carries its final array as one argument. These
// nonvariadic signatures therefore use exactly the ordinary argument path.
func (b *expressionBinder) bindTextObjectFunction(raw parser.FunctionExpr, name string, visible bool) (BoundExpr, error) {
	var small [2]comparisonOperand
	var smallNames [2]string
	var operands []comparisonOperand
	var names []string
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
		names = smallNames[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, r := range raw.Args {
		o, err := b.stringOperand(r, false)
		if err != nil {
			return nil, err
		}
		if names != nil {
			names[i] = "unknown"
			if d := o.descriptor(); d != nil {
				names[i] = d.DiagnosticName()
			}
		}
		if operands != nil {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	missing := func() (BoundExpr, error) {
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	if !visible || len(raw.Args) < 1 || len(raw.Args) > 2 {
		return missing()
	}
	target := b.typ(1009)
	for _, o := range operands {
		if o.descriptor() != nil {
			if _, err := sqltype.PlanCoercion(o.node.ResultUsage(), target.Usage(), sqltype.Implicit); err != nil {
				return missing()
			}
		}
	}
	args := make([]BoundExpr, len(operands))
	var usages [2]sqltype.Usage
	properties := ExprProperties{Volatility: Immutable}
	for i, o := range operands {
		arg, err := b.likeOperand(o, target)
		if err != nil {
			return nil, err
		}
		args[i], usages[i] = arg, arg.ResultUsage()
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		properties.DomainDependent = properties.DomainDependent || p.DomainDependent
		properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.RuntimeDomainConversion
	}
	result := sqltype.JSON
	if name == "jsonb_object" {
		result = sqltype.JSONB
	}
	usage := b.typ(result).Usage()
	plan, err := sqltype.BindBinary(name, usage, usages[:len(args)]...)
	if err != nil {
		return nil, err
	}
	return binaryExpression{expressionInfo: b.info(usage, raw.Pos, properties), plan: plan, args: args}, nil
}
