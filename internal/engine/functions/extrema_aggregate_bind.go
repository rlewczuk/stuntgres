package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func bindExtrema(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, error) {
	var singleType [1]string
	types := singleType[:]
	if len(raw.Args) != 1 {
		types = make([]string, len(raw.Args))
	}
	var operand Argument
	// Child errors precede arity, namespace, and signature diagnostics.
	for i, child := range raw.Args {
		bound, err := comparisonOperand(b, child)
		if err != nil {
			return nil, err
		}
		operand = bound
		types[i], err = b.DomainTypeName(bound.Usage.Type)
		if err != nil {
			return nil, err
		}
	}
	filter, err := b.AnalyzeAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	arg, err := resolveExtremaSignature(b, raw, name, operand, types)
	if err != nil {
		return nil, err
	}
	selected := arg.ResultUsage().Type
	usage := arg.ResultUsage()
	usage.Typmod = -1
	comparison, err := sqltype.BindComparison(selected, selected, usage.Collation, arg.CollationIndeterminate())
	if err != nil {
		return nil, err
	}
	operation := AggregateMin
	if raw.Name == "max" {
		operation = AggregateMax
	}
	provider := NewExtremaProvider(usage, comparison, operation == AggregateMax)
	call := AggregateCall{Provider: provider, Argument: arg, Operation: operation}
	call.Routine, err = b.BuiltinRoutineReference(AggregateRoutineProvider, raw.Name, selected.ID())
	if err != nil {
		return nil, err
	}
	return b.FinishAggregate(raw, call, filter, inputCollatedInfo(b, usage, raw.Pos, arg))
}

// inputCollatedInfo is the info of an aggregate whose result is its input:
// stage-dependent, with the input's parameter dependence, volatility and
// collation state.
func inputCollatedInfo(b Binder, usage sqltype.Usage, pos parser.Position, arg expr.Expr) expr.Info {
	properties := arg.Properties()
	info := b.Info(usage, pos, expr.Properties{StageDependent: true, ParameterDependent: properties.ParameterDependent, Volatility: properties.Volatility})
	info.CollationConflict, info.CollationExplicit = arg.CollationIndeterminate(), arg.ExplicitCollation()
	return info
}

func resolveExtremaSignature(b Binder, raw parser.AggregateExpr, name parser.ObjectName, operand Argument, types []string) (expr.Expr, error) {
	if len(raw.Args) > 100 {
		return nil, aggregateLimitError(raw.Pos)
	}
	visible, err := b.BuiltinVisible(name)
	if err != nil {
		return nil, err
	}
	if !visible || raw.Star || len(raw.Args) != 1 {
		return nil, UnnotedResolutionError(b, name, raw.Pos, types, false)
	}
	selected := b.Type(sqltype.Text)
	if typ := operand.Usage.Type; typ != nil {
		base := typ.BaseType()
		switch base.ID() {
		case sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Oid, sqltype.Float4, sqltype.Float8, sqltype.Numeric,
			sqltype.Text, sqltype.BPChar, sqltype.Date, sqltype.Time, sqltype.Timetz, sqltype.Timestamp, sqltype.Timestamptz, sqltype.Interval, sqltype.Bytea:
			selected = base
		case sqltype.VarChar, sqltype.Name, sqltype.Char:
			selected = b.Type(sqltype.Text)
		case sqltype.Regclass:
			selected = b.Type(sqltype.Oid)
		case sqltype.NameArray, sqltype.PgNodeTree:
			return nil, expr.ErrorAt("0A000", raw.Name+" for "+base.DiagnosticName()+" is unsupported", raw.Pos)
		default:
			return nil, UnnotedResolutionError(b, name, raw.Pos, types, false)
		}
	}
	return coerceConditional(b, operand, selected.Usage(), raw.Name)
}

func windowExtrema(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, sqltype.Usage, error) {
	operands, types, err := windowAggregateOperands(b, raw, domainTypeName)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	arg, err := resolveExtremaSignature(b, raw, name, firstOperand(operands), types)
	if err != nil {
		return arg, sqltype.Usage{}, err
	}
	usage := arg.ResultUsage()
	usage.Typmod = -1
	return arg, usage, nil
}
