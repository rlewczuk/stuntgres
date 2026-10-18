package engine

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindBinaryFunction(b functions.Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var operands []functions.Argument
	if len(raw.Args) <= 100 {
		operands = make([]functions.Argument, len(raw.Args))
	}
	for i, r := range raw.Args {
		o, err := b.AnalyzeArgument(r, functions.BinaryArgumentAnalysis(name, i))
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	access := name == "get_bit" || name == "set_bit"
	if b.DefaultConsumer() && !access && !functions.BinaryDefaultAllowed(name, len(raw.Args)) && !functions.BinaryBitCandidate(name, raw.Variadic, operands) {
		return nil, expr.ErrorAt("0A000", "functions are unsupported", raw.Pos)
	}
	prepared := functions.PrepareBinary(name, visible, raw.Variadic, b.DefaultConsumer(), operands)
	var types functions.BinaryTypes
	functions.SupplyBinaryTypes(b, prepared.Demand, &types)
	selection := functions.ResolveBinary(prepared, name, operands, &types)
	if selection.Resolution != functions.Resolved {
		names := make([]string, len(operands))
		for i, operand := range operands {
			names[i] = "unknown"
			if operand.Usage.Type != nil {
				names[i] = operand.Usage.Type.DiagnosticName()
			}
		}
		if selection.Resolution == functions.Ambiguous {
			return nil, functions.ResolutionError(raw.Name, raw.Pos, names, true)
		}
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if b.DefaultConsumer() && access && selection.Family != sqltype.Bit {
		return nil, expr.ErrorAt("0A000", "functions are unsupported", raw.Pos)
	}
	switch selection.Route {
	case functions.BinaryFunctionsUnsupported:
		return nil, expr.ErrorAt("0A000", "functions are unsupported", raw.Pos)
	case functions.BinaryPosixSubstring:
		return functions.BindPosixSubstring(b, raw, operands)
	case functions.BinarySimilarSubstring:
		return functions.BindSimilarSubstring(b, raw, operands)
	case functions.BinaryTextSubstring:
		return functions.FinishString(b, raw, visible, operands)
	case functions.BinaryTextUnsupported:
		return nil, expr.ErrorAt("0A000", "text overloads of "+name+" are unsupported", raw.Pos)
	}
	return finishBinary(b, name, selection.Family, raw.Pos, operands, raw.Syntax, &types)
}

func finishBinary(b functions.Binder, name string, family sqltype.ID, pos parser.Position, operands []functions.Argument, syntax parser.FunctionSyntax, types *functions.BinaryTypes) (expr.Expr, error) {
	functions.SupplyBinaryTypes(b, functions.BinaryArgumentDemand(name, family, operands, false), types)
	args, err := functions.CoerceBinaryArguments(b, name, family, operands, types)
	if err != nil {
		return nil, err
	}
	properties := expr.Properties{Volatility: expr.Immutable}
	if functions.BinaryIntrinsicStable(name) {
		properties.Volatility = expr.Stable
	}
	for _, arg := range args {
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	functions.SupplyBinaryTypes(b, functions.BinaryResultDemand(name, family), types)
	id := functions.BinaryResultID(name, family)
	usage := types.Usage(id)
	info := b.Info(usage, pos, properties)
	if functions.BinaryIntrinsicStable(name) {
		demand := true
		for _, arg := range args {
			if b.LiteralNull(arg) {
				demand = false
				break
			}
		}
		if demand {
			b.NoteVisibilitySnapshot()
		}
	}
	inputInfo, err := b.CollatedInfo(b.Info(types.Text, pos, properties), args...)
	if err != nil {
		return nil, err
	}
	if id == sqltype.Text {
		info = inputInfo
	}
	node, err := functions.NewBinary(info, name, args, syntax)
	if err != nil {
		return nil, err
	}
	var formal [4]sqltype.ID
	for i := range args {
		formal[i] = functions.BinaryArgumentID(name, i, family)
	}
	if name == "||" {
		info, err = b.BuiltinOperatorRoutineInfo(info, name, formal[0], formal[1])
	} else {
		info, err = b.BuiltinRoutineInfo(info, functions.BinaryRoutineProvider, name, formal[:len(args)]...)
	}
	if err != nil {
		return nil, err
	}
	node.Info = info
	return node, nil
}
