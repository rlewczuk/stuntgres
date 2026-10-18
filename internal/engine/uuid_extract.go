package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindUUIDExtract(b functions.Binder, raw parser.FunctionExpr) (BoundExpr, error) {
	var small [1]functions.Argument
	var operands []functions.Argument
	if len(raw.Args) <= 1 {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]functions.Argument, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = arg
		}
	}
	// The existing scalar syntax envelope owns child/FILTER/count/label order.
	if err := validateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := functions.UUIDExtractFunction(name)
	valid := visible && kind != 0 && len(operands) == 1 && len(raw.ArgNames) == 0 && !raw.Variadic
	if valid && operands[0].Usage.Type != nil {
		valid = operands[0].Usage.Type.BaseType() == b.Type(sqltype.UUID)
	}
	if !valid {
		names := make([]string, len(operands))
		for i, arg := range operands {
			names[i] = "unknown"
			if arg.Usage.Type != nil {
				names[i] = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = functionNamedType(raw, i, names[i])
		}
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if err := rangeInspectDecorationError(raw); err != nil {
		return nil, err
	}
	arg, err := b.CoerceArgument(operands[0], functions.CoercionRequest{Intent: functions.ConditionalArgument, Target: b.Type(sqltype.UUID).Usage(), Label: name})
	if err != nil {
		return nil, err
	}
	properties := expr.MergeProperties(expr.IntrinsicProperties(functions.UUIDExtractIntrinsicRule()), arg.Properties())
	return functions.NewUUIDExtract(b.Info(b.Type(kind.Result()).Usage(), raw.Pos, properties), kind, arg)
}
