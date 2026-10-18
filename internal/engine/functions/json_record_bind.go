package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func jsonRecordFunction(name string) bool {
	return name == "json_populate_record" || name == "json_populate_recordset" || name == "json_to_record" || name == "json_to_recordset"
}

func bindJSONRecord(b Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	populate := name == "json_populate_record" || name == "json_populate_recordset"
	var operandStorage [2]Argument
	operands := operandStorage[:min(len(raw.Args), len(operandStorage))]
	var nameStorage [100]string
	names := nameStorage[:min(len(raw.Args), len(nameStorage))]
	for i, r := range raw.Args {
		o, err := b.AnalyzeArgument(r, AnalysisRequest{Intent: StringOtherArgument})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = o
		}
		if i < len(names) {
			names[i] = "unknown"
			if o.Usage.Type != nil {
				names[i] = b.DiagnosticTypeName(o.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, names[i])
		}
	}
	if err := b.AnalyzeFilter(raw.Filter); err != nil {
		return nil, err
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	missing := func() (expr.Expr, error) { return nil, b.SignatureError(raw.Name, raw.Pos, names) }
	count, baseIndex, jsonIndex := 1, -1, 0
	if populate {
		count, baseIndex, jsonIndex = 2, 0, 1
	}
	if !visible || raw.ResolutionOnly || raw.Variadic || len(raw.Args) != count {
		return missing()
	}
	if len(raw.ArgNames) != 0 {
		if !populate {
			return missing()
		}
		seen := [2]bool{}
		for i := range operands {
			index := i
			if i < len(raw.ArgNames) && raw.ArgNames[i].Text != "" {
				switch raw.ArgNames[i].Text {
				case "base":
					index = 0
				case "from_json":
					index = 1
				default:
					return missing()
				}
			}
			if seen[index] {
				return missing()
			}
			seen[index] = true
			if index == 0 {
				baseIndex = i
			} else {
				jsonIndex = i
			}
		}
	}
	if ResolveJSONArgument(operands[jsonIndex].Usage, b.Type(sqltype.JSON).Usage()) != Resolved {
		return missing()
	}
	if populate && operands[baseIndex].Usage.Type == nil {
		return nil, expr.ErrorAt("42804", "could not determine polymorphic type because input has type unknown", parser.Position{})
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	args := make([]expr.Expr, count)
	properties := expr.Properties{Volatility: expr.Stable}
	for i, operand := range operands {
		arg := operand.Expression
		if i == jsonIndex {
			var err error
			arg, err = b.CoerceArgument(operand, CoercionRequest{Intent: LikeArgument, Target: b.Type(sqltype.JSON).Usage()})
			if err != nil {
				return nil, err
			}
		}
		args[i] = arg
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		properties.DomainDependent = properties.DomainDependent || p.DomainDependent
		properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.RuntimeDomainConversion
	}
	usage := sqltype.DeferredRecord().Usage()
	if populate {
		usage = args[baseIndex].ResultUsage()
	}
	if name == "json_populate_recordset" || name == "json_to_recordset" {
		if err := b.AdmitSetReturning(raw.Pos); err != nil {
			return nil, err
		}
		properties.StageDependent = true
	}
	properties.DomainDependent = properties.DomainDependent || sqltype.ContainsDomainChecks(usage.Type)
	properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || sqltype.ContainsDomainChecks(usage.Type)
	info := b.Info(usage, raw.Pos, properties)
	record, err := NewJSONRecord(info, name, baseIndex, args)
	if err != nil {
		return nil, err
	}
	if populate {
		info, err = b.BuiltinRoutineInfo(info, JSONRoutineProvider, name, 2283, sqltype.JSON, sqltype.Bool)
	} else {
		info, err = b.BuiltinRoutineInfo(info, JSONRoutineProvider, name, sqltype.JSON)
	}
	if err != nil {
		return nil, err
	}
	record.Info = info
	return record, nil
}

func bindJSONBRecord(b Binder, raw parser.FunctionExpr, visible bool) (expr.Expr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	populate := name == "jsonb_populate_record" || name == "jsonb_populate_recordset" || name == "jsonb_populate_record_valid"
	count := 1
	if populate {
		count = 2
	}
	operands := make([]Argument, len(raw.Args))
	names := make([]string, len(raw.Args))
	for i, arg := range raw.Args {
		intent := StringOtherArgument
		if populate && i == 0 {
			intent = RecordBaseArgument
		}
		o, err := b.AnalyzeArgument(arg, AnalysisRequest{Intent: intent})
		if err != nil {
			return nil, err
		}
		operands[i] = o
		names[i] = "unknown"
		if o.Usage.Type != nil {
			names[i] = b.DiagnosticTypeName(o.Usage.Type)
		}
		names[i] = NamedArgumentType(raw, i, names[i])
	}
	if err := b.AnalyzeFilter(raw.Filter); err != nil {
		return nil, err
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	missing := func() (expr.Expr, error) { return nil, b.SignatureError(raw.Name, raw.Pos, names) }
	if !visible || len(operands) != count || len(raw.ArgNames) != 0 || raw.Variadic {
		return missing()
	}
	if populate && operands[0].Usage.Type == nil {
		return nil, expr.ErrorAt("42804", "could not determine polymorphic type because input has type unknown", parser.Position{})
	}
	input := b.Type(sqltype.JSONB).Usage()
	if ResolveJSONArgument(operands[count-1].Usage, input) != Resolved {
		return missing()
	}
	args := make([]expr.Expr, count)
	if populate {
		args[0] = operands[0].Expression
	}
	var err error
	args[count-1], err = b.CoerceArgument(operands[count-1], CoercionRequest{Intent: LikeArgument, Target: input})
	if err != nil {
		return nil, err
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	var result sqltype.Usage
	unresolved := false
	if populate {
		result = operands[0].Usage
		unresolved = result.Type.ID() == 2249
	} else {
		shape, err := sqltype.RecordShape(nil)
		if err != nil {
			return nil, err
		}
		result = shape.Usage()
		unresolved = true
	}
	if err := b.CaptureTypeInput(result.Type); err != nil {
		return nil, err
	}
	if name == "jsonb_populate_record_valid" {
		result = b.Type(sqltype.Bool).Usage()
	}
	properties := expr.Properties{Volatility: expr.Stable, RuntimeDomainConversion: true}
	for _, arg := range args {
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if name == "jsonb_populate_recordset" || name == "jsonb_to_recordset" {
		if err := b.AdmitSetReturning(raw.Pos); err != nil {
			return nil, err
		}
		properties.StageDependent = true
	}
	info := b.Info(result, raw.Pos, properties)
	call := JSONBRecord{Info: info, Name: name, Args: args, Unresolved: unresolved}
	call, err = call.Reconstruct(call.Info, args)
	if err != nil {
		return nil, err
	}
	if populate {
		info, err = b.BuiltinRoutineInfo(info, JSONRoutineProvider, name, 2283, sqltype.JSONB)
	} else {
		info, err = b.BuiltinRoutineInfo(info, JSONRoutineProvider, name, sqltype.JSONB)
	}
	if err != nil {
		return nil, err
	}
	call.Info = info
	return call, nil
}
