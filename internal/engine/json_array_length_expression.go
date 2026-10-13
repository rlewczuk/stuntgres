package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindJSONArrayLengthFunction(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	var operand comparisonOperand
	var singleName [1]string
	var names []string
	if len(raw.Args) <= 1 {
		names = singleName[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	// Transform every supplied operand before diagnosing the signature, as
	// ordinary function resolution does, retaining only the unary operand.
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
		if i == 0 {
			operand = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	missing := func() (BoundExpr, error) {
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	if !visible || len(raw.Args) != 1 {
		return missing()
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	id := sqltype.JSON
	if name == "jsonb_array_length" {
		id = sqltype.JSONB
	}
	target := b.typ(id)
	if operand.descriptor() != nil {
		if _, err := sqltype.PlanCoercion(operand.node.ResultUsage(), target.Usage(), sqltype.Implicit); err != nil {
			return missing()
		}
	}
	arg, err := b.likeOperand(operand, target)
	if err != nil {
		return nil, err
	}
	usage := b.typ(sqltype.Int4).Usage()
	plan, err := sqltype.BindBinary(name, usage, arg.ResultUsage())
	if err != nil {
		return nil, err
	}
	return binaryExpression{expressionInfo: b.info(usage, raw.Pos, arg.Properties()), plan: plan, args: []BoundExpr{arg}}, nil
}
