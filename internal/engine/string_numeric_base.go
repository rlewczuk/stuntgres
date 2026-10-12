package engine

import (
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Both integer candidates are considered before unknown input is finalized.
// Neither is a preferred numeric type, so SMALLINT and UNKNOWN remain ambiguous.
func (b *expressionBinder) finishStringNumericBase(raw parser.FunctionExpr, visible bool, operands []comparisonOperand) (BoundExpr, error) {
	var target *sqltype.Descriptor
	matches := 0
	if visible && len(operands) == 1 {
		o := operands[0]
		for _, id := range [...]sqltype.ID{sqltype.Int4, sqltype.Int8} {
			candidate := b.typ(id)
			if d := o.descriptor(); d != nil {
				if d.BaseType().ID() == id {
					target, matches = candidate, 1
					break
				}
				if _, err := sqltype.PlanCoercion(o.node.ResultUsage(), candidate.Usage(), sqltype.Implicit); err != nil {
					continue
				}
			}
			target = candidate
			matches++
		}
	}
	if matches != 1 {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = "unknown"
			if d := o.descriptor(); d != nil {
				names[i] = d.DiagnosticName()
			}
		}
		return nil, b.functionResolutionErrorForTypes(raw.Name, raw.Pos, names, matches > 1)
	}
	arg, err := b.finalizeOperand(operands[0], target)
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
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	return b.stringNode(name, b.typ(sqltype.Text).Usage(), raw.Pos, []BoundExpr{arg}, [4]bool{}, raw.StringSyntax)
}
