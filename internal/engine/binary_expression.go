package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindBinaryFunction(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	var operands []functions.Argument[BoundExpr]
	if len(raw.Args) <= 100 {
		operands = make([]functions.Argument[BoundExpr], len(raw.Args))
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
	b.supplyBinaryTypes(prepared.Demand, &types)
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
		return b.bindPosixSubstring(raw, operands)
	case functions.BinarySimilarSubstring:
		return b.bindSimilarSubstring(raw, operands)
	case functions.BinaryTextSubstring:
		return b.finishStringFunction(raw, visible, operands)
	case functions.BinaryTextUnsupported:
		return nil, expressionError("0A000", "text overloads of "+name+" are unsupported", raw.Pos)
	}
	return b.finishBinary(name, selection.Family, raw.Pos, operands, raw.Syntax, &types)
}

func (b *expressionBinder) finishBinary(name string, family sqltype.ID, pos parser.Position, operands []functions.Argument[BoundExpr], syntax parser.FunctionSyntax, types *functions.BinaryTypes) (BoundExpr, error) {
	b.supplyBinaryTypes(functions.BinaryArgumentDemand(name, family, operands, false), types)
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
	b.supplyBinaryTypes(functions.BinaryResultDemand(name, family), types)
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
	node, err := functions.NewBinary[BoundExpr, *EvalContext](functionInfo(info), name, args, syntax)
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
	node.Info = functionInfo(info)
	return node, nil
}

func (b *expressionBinder) supplyBinaryTypes(demand functions.BinaryDemand, types *functions.BinaryTypes) {
	if demand == 0 {
		return
	}
	if demand.Needs(sqltype.Bytea) && types.Bytea.Type == nil {
		types.Bytea = b.typ(sqltype.Bytea).Usage()
	}
	if demand.Needs(sqltype.Bit) && types.Bit.Type == nil {
		types.Bit = b.typ(sqltype.Bit).Usage()
	}
	if demand.Needs(sqltype.VarBit) && types.VarBit.Type == nil {
		types.VarBit = b.typ(sqltype.VarBit).Usage()
	}
	if demand.Needs(sqltype.Int4) && types.Integer.Type == nil {
		types.Integer = b.typ(sqltype.Int4).Usage()
	}
	if demand.Needs(sqltype.Int8) && types.Bigint.Type == nil {
		types.Bigint = b.typ(sqltype.Int8).Usage()
	}
	if demand.Needs(sqltype.Name) && types.Name.Type == nil {
		types.Name = b.typ(sqltype.Name).Usage()
	}
	if demand.Needs(sqltype.Text) && types.Text.Type == nil {
		types.Text = b.typ(sqltype.Text).Usage()
	}
}
