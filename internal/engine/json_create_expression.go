package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindJSONCreate(b functions.Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	kind := functions.JSONCreateKind(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	var operands []functions.Argument
	var small [2]functions.Argument
	if len(raw.Args) <= len(small) {
		operands = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]functions.Argument, len(raw.Args))
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
		return nil, expr.ErrorAt("42601", "positional argument cannot follow named argument", failure.Pos)
	case functions.DuplicateLabel:
		return nil, expr.ErrorAt("42601", fmt.Sprintf("argument name %q used more than once", failure.Name), failure.Pos)
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	var boolean sqltype.Usage
	if !kind.Builder() && len(operands) == 2 && operands[1].Usage.Type != nil {
		boolean = b.Type(sqltype.Bool).Usage()
	}
	if functions.ResolveJSONCreate(kind, raw.Name.Parts[len(raw.Name.Parts)-1].Text, visible, raw.Variadic, raw.ResolutionOnly, len(raw.ArgNames), operands, boolean) != functions.Resolved {
		names := make([]string, len(operands))
		for i, o := range operands {
			names[i] = "unknown"
			if d := o.Usage.Type; d != nil {
				names[i] = b.DiagnosticTypeName(d)
			}
			names[i] = functions.NamedArgumentType(raw, i, names[i])
		}
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	name := kind.Name()
	switch {
	case raw.Star:
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Distinct:
		return nil, expr.ErrorAt("42809", "DISTINCT specified, but "+name+" is not an aggregate function", raw.Pos)
	case len(raw.Order) != 0:
		return nil, expr.ErrorAt("42809", "ORDER BY specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Filter != nil:
		return nil, expr.ErrorAt("42809", "FILTER specified, but "+name+" is not an aggregate function", raw.Pos)
	case raw.Window != nil:
		return nil, expr.ErrorAt("42809", "OVER specified, but "+name+" is not a window function nor an aggregate function", raw.Pos)
	}
	args := make([]expr.Expr, len(operands))
	var unknown [2]uint64
	properties := expr.IntrinsicProperties(functions.JSONCreateIntrinsicRule())
	for i, o := range operands {
		arg := o.Expression
		rawUnknown := functions.JSONCreateRawUnknown(raw.Args[i])
		if rawUnknown {
			unknown[i/64] |= uint64(1) << uint(i%64)
		}
		switch functions.JSONCreateRule(kind, i, o.Usage, rawUnknown) {
		case functions.JSONUnknownPolymorphic:
			return nil, expr.ErrorAt("42804", "could not determine polymorphic type because input has type unknown", parser.Position{})
		case functions.JSONRecordFormal:
			// The fixed RECORD formal supplies an occurrence-owned transport identity,
			// not a promotion of catalog RECORD or an anonymous composite text codec.
			arg, err = b.CoerceArgument(o, functions.CoercionRequest{Intent: functions.RecordFormalArgument})
		case functions.JSONBooleanFormal:
			arg, err = b.CoerceArgument(o, functions.CoercionRequest{Intent: functions.LikeArgument, Target: b.Type(sqltype.Bool).Usage()})
		case functions.JSONTextUnknown:
			arg, err = b.CoerceArgument(o, functions.CoercionRequest{Intent: functions.FinalizeArgument, Target: b.Type(sqltype.Text).Usage()})
		}
		if err != nil {
			return nil, err
		}
		if arg == nil {
			return nil, fmt.Errorf("missing JSON function operand")
		}
		args[i] = arg
		b.RetainUntypedParameter(arg)
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		properties.DomainDependent = properties.DomainDependent || p.DomainDependent
		properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.RuntimeDomainConversion
		if arg.ResultUsage().Type != nil && arg.ResultUsage().Type.ContainsCatalogIdentifier() {
			b.NoteCatalogText()
		}
	}
	if kind.Builder() && raw.Variadic && !functions.JSONCreateVariadicArgumentValid(args[0].ResultUsage()) {
		return nil, expr.ErrorAt("42804", "VARIADIC argument must be an array", raw.Args[0].SourcePosition())
	}
	info := b.Info(b.Type(functions.JSONCreateResult(kind)).Usage(), raw.Pos, properties)
	node, err := functions.NewJSONCreate(info, kind, args, raw.Variadic, unknown)
	if err != nil {
		return nil, err
	}
	var formal [2]sqltype.ID
	count := len(args)
	switch name {
	case "to_json", "to_jsonb":
		formal[0] = sqltype.ID(2283)
	case "array_to_json":
		formal = [2]sqltype.ID{sqltype.ID(2277), sqltype.Bool}
	case "row_to_json":
		formal = [2]sqltype.ID{sqltype.ID(2249), sqltype.Bool}
	default:
		count = 0
		if raw.Variadic || len(args) != 0 {
			formal[0], count = sqltype.ID(2276), 1
		}
	}
	info, err = b.BuiltinRoutineInfo(info, functions.JSONRoutineProvider, name, formal[:count]...)
	if err != nil {
		return nil, err
	}
	node.Info = info
	return node, nil
}
