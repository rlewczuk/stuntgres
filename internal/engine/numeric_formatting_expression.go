package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindNumericFormatting(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	var small [4]functions.Argument
	operands := small[:min(len(raw.Args), len(small))]
	if len(raw.Args) > len(small) && len(raw.Args) <= 100 {
		operands = make([]functions.Argument, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, functions.AnalysisRequest{Intent: functions.StringTextArgument})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if functions.ResolveNumericFormatting(visible, raw.Variadic, operands) != functions.Resolved {
		return nil, b.numericFormattingSignature(raw, operands)
	}
	return functions.NumericFormattingOperands(b, raw, operands, b.typ(sqltype.Text))
}
func (b *expressionBinder) numericFormattingSignature(raw parser.FunctionExpr, operands []functions.Argument) error {
	names := make([]string, len(operands))
	for i, o := range operands {
		names[i] = functions.DiagnosticNameOrUnknown(o.Usage.Type)
	}
	return b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
}
