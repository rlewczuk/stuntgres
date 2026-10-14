package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Both integer candidates are considered before unknown input is finalized.
// Neither is a preferred numeric type, so SMALLINT and UNKNOWN remain ambiguous.
func (b *expressionBinder) finishStringNumericBase(raw parser.FunctionExpr, visible bool, operands []functions.Argument[BoundExpr]) (BoundExpr, error) {
	ids, count := functions.StringNumericBaseCandidates(visible, operands)
	var targets [2]sqltype.Usage
	for i, id := range ids[:count] {
		targets[i] = b.typ(id).Usage()
	}
	target, resolution := functions.SelectStringNumericBase(visible, operands, targets[:count])
	if resolution != functions.Resolved {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = "unknown"
			if d := o.Usage.Type; d != nil {
				names[i] = d.DiagnosticName()
			}
		}
		return nil, b.functionResolutionErrorForTypes(raw.Name, raw.Pos, names, resolution == functions.Ambiguous)
	}
	arg, err := functions.CoerceStringNumericBase(b, operands[0], target)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	return b.stringNode(name, b.typ(sqltype.Text).Usage(), raw.Pos, []BoundExpr{arg}, [4]bool{}, raw.StringSyntax)
}
