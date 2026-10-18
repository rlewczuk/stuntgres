package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func jsonRecordFunction(name string) bool {
	return name == "json_populate_record" || name == "json_populate_recordset" || name == "json_to_record" || name == "json_to_recordset"
}

func (b *expressionBinder) bindJSONRecord(raw parser.FunctionExpr, visible bool) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	populate := name == "json_populate_record" || name == "json_populate_recordset"
	var operandStorage [2]functions.Argument
	operands := operandStorage[:min(len(raw.Args), len(operandStorage))]
	var nameStorage [100]string
	names := nameStorage[:min(len(raw.Args), len(nameStorage))]
	for i, r := range raw.Args {
		o, err := b.AnalyzeArgument(r, functions.AnalysisRequest{Intent: functions.StringOtherArgument})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = o
		}
		if i < len(names) {
			names[i] = "unknown"
			if o.Usage.Type != nil {
				names[i] = b.diagnosticTypeName(o.Usage.Type)
			}
			names[i] = functionNamedType(raw, i, names[i])
		}
	}
	if _, err := b.bindAggregateFilter(parser.AggregateExpr{Filter: raw.Filter}); err != nil {
		return nil, err
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	missing := func() (BoundExpr, error) { return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names) }
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
	if functions.ResolveJSONArgument(operands[jsonIndex].Usage, b.typ(sqltype.JSON).Usage()) != functions.Resolved {
		return missing()
	}
	if populate && operands[baseIndex].Usage.Type == nil {
		return nil, sqlError("42804", "could not determine polymorphic type because input has type unknown")
	}
	decoration, message := "", "not an aggregate function"
	switch {
	case raw.Distinct:
		decoration = "DISTINCT"
	case len(raw.Order) != 0:
		decoration = "ORDER BY"
	case raw.Filter != nil:
		decoration = "FILTER"
	case raw.Window != nil:
		decoration, message = "OVER", "not a window function nor an aggregate function"
	}
	if decoration != "" {
		return nil, expressionError("42809", decoration+" specified, but "+objectNameText(raw.Name)+" is "+message, raw.Pos)
	}
	args := make([]BoundExpr, count)
	properties := ExprProperties{Volatility: Stable}
	for i, operand := range operands {
		arg := operand.Expression
		if i == jsonIndex {
			var err error
			arg, err = b.CoerceArgument(operand, functions.CoercionRequest{Intent: functions.LikeArgument, Target: b.typ(sqltype.JSON).Usage()})
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
		if err := b.stringSetAllowed(raw.Pos); err != nil {
			return nil, err
		}
		switch b.aggregateClause {
		case "SELECT", "GROUP BY", "ORDER BY", "DISTINCT ON", "window definitions":
			b.queryHasTargetSet = true
		}
		properties.StageDependent = true
	}
	properties.DomainDependent = properties.DomainDependent || sqltype.ContainsDomainChecks(usage.Type)
	properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || sqltype.ContainsDomainChecks(usage.Type)
	info := b.info(usage, raw.Pos, properties)
	record, err := functions.NewJSONRecord(functionInfo(info), name, baseIndex, args)
	if err != nil {
		return nil, err
	}
	if populate {
		info, err = b.builtinRoutineInfo(info, jsonRoutineProvider, name, 2283, sqltype.JSON, sqltype.Bool)
	} else {
		info, err = b.builtinRoutineInfo(info, jsonRoutineProvider, name, sqltype.JSON)
	}
	if err != nil {
		return nil, err
	}
	record.Info = functionInfo(info)
	return record, nil
}

// Completion follows ordinary call/signature/SRF analysis, but precedes source
// schema expansion. Explicit definitions are occurrence authorities, not aliases.
func (b *expressionBinder) completeFunctionDefinitions(raw parser.FunctionExpr, call BoundExpr, definitions []parser.FunctionColumnDefinition) (BoundExpr, error) {
	record, isRecord := call.(functions.JSONRecord)
	jsonb, isJSONB := call.(functions.JSONBRecord)
	anonymous := (isRecord || isJSONB) && call.ResultUsage().Type.ID() == 2249
	if len(definitions) == 0 {
		if isRecord && anonymous || jsonbRecordUnresolved(call) {
			return nil, expressionError("42601", `a column definition list is required for functions returning "record"`, raw.Pos)
		}
		return call, nil
	}
	if !anonymous {
		return nil, expressionError("42601", functionColumnDefinitionMessage(call), definitions[0].Name.Pos)
	}
	if isJSONB {
		usage, err := b.bindJSONBRecordDefinition(definitions)
		if err != nil {
			return nil, err
		}
		info := jsonb.Info
		info.Usage = usage
		info.Property.DomainDependent = info.Property.DomainDependent || sqltype.ContainsDomainChecks(usage.Type)
		jsonb.Definitions, jsonb.Unresolved = true, false
		return jsonb.Reconstruct(info, jsonb.Args)
	}
	members := make([]sqltype.CompositeMember, len(definitions))
	seen := make(map[string]bool, len(definitions))
	for i, definition := range definitions {
		if seen[definition.Name.Text] {
			return nil, sqlError("42701", `column name "`+definition.Name.Text+`" specified more than once`)
		}
		seen[definition.Name.Text] = true
		u, err := b.resolveFunctionColumnUsage(definition, true)
		if err != nil {
			return nil, err
		}
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: definition.Name.Text, Usage: u}
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		return nil, err
	}
	info := record.Info
	info.Usage = shape.Usage()
	info.Property.DomainDependent = info.Property.DomainDependent || sqltype.ContainsDomainChecks(shape)
	info.Property.RuntimeDomainConversion = info.Property.RuntimeDomainConversion || sqltype.ContainsDomainChecks(shape)
	record.Defined = true
	return record.Reconstruct(info, record.Args)
}

// Both record providers resolve members through the same captured input
// authority. Duplicate-name ordering remains with each provider's member loop.
func (b *expressionBinder) resolveFunctionColumnUsage(definition parser.FunctionColumnDefinition, unknownNull bool) (sqltype.Usage, error) {
	u, err := b.engine.resolveAnnotationType(definition.Type, b.op, unknownNull)
	if err != nil {
		return sqltype.Usage{}, err
	}
	u, err = b.engine.resolveColumnCollation(u, definition.Collation, b.op)
	if err != nil {
		return sqltype.Usage{}, err
	}
	if err := b.captureTypeDiagnosticInput(u.Type); err != nil {
		return sqltype.Usage{}, err
	}
	if u.Type.ContainsCatalogIdentifier() {
		if err := b.captureElementInput(); err != nil {
			return sqltype.Usage{}, err
		}
	}
	return u, nil
}

func functionDefinitionError(message, hint string, pos parser.Position) error {
	err := expressionError("42601", message, pos).(*sqlstate.Error)
	err.Hint = hint
	return err
}
