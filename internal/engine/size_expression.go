package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Size overloads have no preferred numeric candidate. Analyze children before
// resolving signatures, while retaining unknowns for occurrence-owned inference.
func (b *expressionBinder) bindSize(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var operand comparisonOperand
	var names []string
	if len(raw.Args) <= 100 && len(raw.Args) != 1 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		o, err := b.stringOperand(child, name == "pg_size_bytes")
		if err != nil {
			return nil, err
		}
		if len(raw.Args) == 1 {
			operand = o
		} else if names != nil {
			names[i] = "unknown"
			if d := o.descriptor(); d != nil {
				names[i] = d.DiagnosticName()
			}
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	var target *sqltype.Descriptor
	matches := 0
	if visible && len(raw.Args) == 1 {
		candidates := [2]sqltype.ID{sqltype.Text}
		count := 1
		if name == "pg_size_pretty" {
			candidates, count = [2]sqltype.ID{sqltype.Int8, sqltype.Numeric}, 2
		}
		for _, id := range candidates[:count] {
			candidate := b.typ(id)
			if d := operand.descriptor(); d != nil {
				if d.BaseType().ID() == id {
					target, matches = candidate, 1
					break
				}
				if _, err := sqltype.PlanCoercion(operand.node.ResultUsage(), candidate.Usage(), sqltype.Implicit); err != nil {
					continue
				}
			}
			target = candidate
			matches++
		}
	}
	if matches != 1 {
		if len(raw.Args) == 1 {
			typ := "unknown"
			if d := operand.descriptor(); d != nil {
				typ = d.DiagnosticName()
			}
			names = []string{typ}
		}
		return nil, functionResolutionErrorForTypes(raw.Name, raw.Pos, names, matches > 1)
	}
	arg, err := b.finalizeOperand(operand, target)
	if err != nil {
		return nil, err
	}
	if arg.ResultUsage() != target.Usage() {
		plan, err := sqltype.PlanCoercion(arg.ResultUsage(), target.Usage(), sqltype.Implicit)
		if err != nil {
			return nil, err
		}
		arg = coercionExpression{b.info(target.Usage(), arg.SourcePosition(), arg.Properties()), arg, plan, sqltype.Implicit}
	}
	result := b.typ(sqltype.Text).Usage()
	if name == "pg_size_bytes" {
		result = b.typ(sqltype.Int8).Usage()
	}
	plan, err := sqltype.BindMath(name, result, target.Usage())
	if err != nil {
		return nil, err
	}
	return mathExpression{b.info(result, raw.Pos, arg.Properties()), plan, []BoundExpr{arg}}, nil
}
