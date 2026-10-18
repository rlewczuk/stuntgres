package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindBinaryFunction(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
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
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	access := name == "get_bit" || name == "set_bit"
	if b.defaultConsumer && !access && !functions.BinaryDefaultAllowed(name, len(raw.Args)) && !functions.BinaryBitCandidate(name, raw.Variadic, operands) {
		return nil, expressionError("0A000", "functions are unsupported", raw.Pos)
	}
	prepared := functions.PrepareBinary(name, visible, raw.Variadic, b.defaultConsumer, operands)
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
			return nil, functionResolutionErrorForTypes(raw.Name, raw.Pos, names, true)
		}
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	if b.defaultConsumer && access && selection.Family != sqltype.Bit {
		return nil, expressionError("0A000", "functions are unsupported", raw.Pos)
	}
	switch selection.Route {
	case functions.BinaryFunctionsUnsupported:
		return nil, expressionError("0A000", "functions are unsupported", raw.Pos)
	case functions.BinaryPosixSubstring:
		return functions.BindPosixSubstring(b, raw, operands)
	case functions.BinarySimilarSubstring:
		return functions.BindSimilarSubstring(b, raw, operands)
	case functions.BinaryTextSubstring:
		return functions.FinishString(b, raw, visible, operands)
	case functions.BinaryTextUnsupported:
		return nil, expressionError("0A000", "text overloads of "+name+" are unsupported", raw.Pos)
	}
	return b.finishBinary(name, selection.Family, raw.Pos, operands, raw.Syntax, &types)
}

func (b *expressionBinder) finishBinary(name string, family sqltype.ID, pos parser.Position, operands []functions.Argument, syntax parser.FunctionSyntax, types *functions.BinaryTypes) (BoundExpr, error) {
	functions.SupplyBinaryTypes(b, functions.BinaryArgumentDemand(name, family, operands, false), types)
	args, err := functions.CoerceBinaryArguments(b, name, family, operands, types)
	if err != nil {
		return nil, err
	}
	properties := ExprProperties{Volatility: Immutable}
	if functions.BinaryIntrinsicStable(name) {
		properties.Volatility = Stable
	}
	for _, arg := range args {
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		properties.DomainDependent = properties.DomainDependent || p.DomainDependent
		properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.RuntimeDomainConversion
	}
	functions.SupplyBinaryTypes(b, functions.BinaryResultDemand(name, family), types)
	id := functions.BinaryResultID(name, family)
	usage := types.Usage(id)
	info := b.info(usage, pos, properties)
	if functions.BinaryIntrinsicStable(name) {
		demand := true
		for _, arg := range args {
			if randomLiteralNull(arg) {
				demand = false
				break
			}
		}
		b.visibilitySnapshot = b.visibilitySnapshot || demand
	}
	inputInfo, err := b.collatedInfo(b.info(types.Text, pos, properties), args...)
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
		info, err = b.builtinOperatorRoutineInfo(info, name, formal[0], formal[1])
	} else {
		info, err = b.builtinRoutineInfo(info, binaryRoutineProvider, name, formal[:len(args)]...)
	}
	if err != nil {
		return nil, err
	}
	node.Info = info
	return node, nil
}
