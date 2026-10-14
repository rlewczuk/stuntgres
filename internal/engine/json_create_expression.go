package engine

import (
	"fmt"
	"stuntgres/internal/engine/functions"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (b *expressionBinder) bindJSONCreate(raw parser.FunctionExpr) (BoundExpr, error) {
	kind := functions.JSONCreateKind(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	var operands []functions.Argument[BoundExpr]
	var small [2]functions.Argument[BoundExpr]
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]functions.Argument[BoundExpr], len(raw.Args))
	}
	for i, r := range raw.Args {
		o, err := b.AnalyzeArgument(r, functions.AnalysisRequest{Intent: functions.StringOtherArgument})
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = o
		}
	}
	if len(raw.ArgNames) != 0 && len(raw.ArgNames) != len(raw.Args) {
		return nil, fmt.Errorf("invalid JSON function argument labels")
	}
	switch failure := functions.AnalyzeLabels(raw.ArgNames, raw.Args); failure.Kind {
	case functions.PositionalAfterNamed:
		return nil, expressionError("42601", "positional argument cannot follow named argument", failure.Pos)
	case functions.DuplicateLabel:
		return nil, expressionError("42601", fmt.Sprintf("argument name %q used more than once", failure.Name), failure.Pos)
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	var boolean sqltype.Usage
	if !kind.Builder() && len(operands) == 2 && operands[1].Usage.Type != nil {
		boolean = b.typ(sqltype.Bool).Usage()
	}
	if functions.ResolveJSONCreate(kind, raw.Name.Parts[len(raw.Name.Parts)-1].Text, visible, raw.Variadic, raw.ResolutionOnly, len(raw.ArgNames), operands, boolean) != functions.Resolved {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = "unknown"
			if d := o.Usage.Type; d != nil {
				names[i] = b.diagnosticTypeName(d)
			}
			names[i] = functionNamedType(raw, i, names[i])
		}
		return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names)
	}
	name := kind.Name()
	switch {
	case raw.Star:
		return nil, expressionError("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Distinct:
		return nil, expressionError("42809", "DISTINCT specified, but "+name+" is not an aggregate function", raw.Pos)
	case len(raw.Order) != 0:
		return nil, expressionError("42809", "ORDER BY specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Filter != nil:
		return nil, expressionError("42809", "FILTER specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Window != nil:
		return nil, expressionError("42809", "OVER specified, but "+name+" is not a window function nor an aggregate function", raw.Pos)
	}
	args := make([]BoundExpr, len(operands))
	var unknown [2]uint64
	properties := functionIntrinsicProperties(functions.JSONCreateIntrinsicRule())
	for i, o := range operands {
		arg := o.Expression
		rawUnknown := functions.JSONCreateRawUnknown(raw.Args[i])
		if rawUnknown {
			unknown[i/64] |= uint64(1) << uint(i%64)
		}
		switch functions.JSONCreateRule(kind, i, o.Usage, rawUnknown) {
		case functions.JSONUnknownPolymorphic:
			return nil, sqlError("42804", "could not determine polymorphic type because input has type unknown")
		case functions.JSONRecordFormal:
			// The fixed RECORD formal supplies an occurrence-owned transport identity,
			// not a promotion of catalog RECORD or an anonymous composite text codec.
			var record *sqltype.Descriptor
			if p, ok := o.Expression.(parameterExpression); ok {
				record = b.parameters[p.number].Type
			}
			if record == nil {
				record, err = sqltype.RecordShape(nil)
				if err != nil {
					return nil, err
				}
			}
			arg, err = b.CoerceArgument(o, functions.CoercionRequest{Intent: functions.FinalizeArgument, Target: record.Usage()})
		case functions.JSONBooleanFormal:
			arg, err = b.CoerceArgument(o, functions.CoercionRequest{Intent: functions.LikeArgument, Target: b.typ(sqltype.Bool).Usage()})
		case functions.JSONTextUnknown:
			arg, err = b.CoerceArgument(o, functions.CoercionRequest{Intent: functions.FinalizeArgument, Target: b.typ(sqltype.Text).Usage()})
		}
		if err != nil {
			return nil, err
		}
		if arg == nil {
			return nil, fmt.Errorf("missing JSON function operand")
		}
		args[i] = arg
		if parameter, ok := arg.(parameterExpression); ok && parameter.usage.Type == nil {
			b.anyParameters = append(b.anyParameters, parameter)
		}
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		properties.DomainDependent = properties.DomainDependent || p.DomainDependent
		properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.RuntimeDomainConversion
		if arg.ResultUsage().Type != nil && arg.ResultUsage().Type.ContainsCatalogIdentifier() {
			b.regclassConcat = true
		}
	}
	if kind.Builder() && raw.Variadic && !functions.JSONCreateVariadicArgumentValid(args[0].ResultUsage()) {
		return nil, expressionError("42804", "VARIADIC argument must be an array", raw.Args[0].SourcePosition())
	}
	return functions.NewJSONCreate[BoundExpr, *EvalContext](functionInfo(b.info(b.typ(functions.JSONCreateResult(kind)).Usage(), raw.Pos, properties)), kind, args, raw.Variadic, unknown)
}
