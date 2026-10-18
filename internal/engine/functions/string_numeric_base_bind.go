package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Both integer candidates are considered before unknown input is finalized.
// Neither is a preferred numeric type, so SMALLINT and UNKNOWN remain ambiguous.
func finishStringNumericBase(b Binder, raw parser.FunctionExpr, visible bool, operands []Argument) (expr.Expr, error) {
	ids, count := StringNumericBaseCandidates(visible, operands)
	var targets [2]sqltype.Usage
	for i, id := range ids[:count] {
		targets[i] = b.Type(id).Usage()
	}
	target, resolution := SelectStringNumericBase(visible, operands, targets[:count])
	if resolution != Resolved {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = "unknown"
			if d := o.Usage.Type; d != nil {
				names[i] = d.DiagnosticName()
			}
		}
		return nil, NotedResolutionError(b, raw.Name, raw.Pos, names, resolution == Ambiguous)
	}
	arg, err := CoerceStringNumericBase(b, operands[0], target)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	return StringNode(b, name, b.Type(sqltype.Text).Usage(), raw.Pos, []expr.Expr{arg}, [4]bool{}, raw.StringSyntax)
}
