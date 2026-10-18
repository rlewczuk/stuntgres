package functions

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

var statisticalNames = [...]string{
	"var_pop", "var_samp", "variance", "stddev_pop", "stddev_samp", "stddev", "regr_slope", "regr_sxx", "regr_sxy", "regr_syy",
	"corr", "covar_pop", "covar_samp", "regr_avgx", "regr_avgy", "regr_count", "regr_intercept", "regr_r2",
}

// regressionFinals selects the two-argument regression aggregates' final
// functions; the variance family is unary.
var regressionFinals = map[string]sqltype.RegressionOperation{
	"regr_slope": sqltype.RegressionSlope, "regr_sxx": sqltype.RegressionSxx, "regr_sxy": sqltype.RegressionSxy,
	"regr_syy": sqltype.RegressionSyy, "corr": sqltype.RegressionCorr, "covar_pop": sqltype.RegressionCovarPop,
	"covar_samp": sqltype.RegressionCovarSamp, "regr_avgx": sqltype.RegressionAvgx, "regr_avgy": sqltype.RegressionAvgy,
	"regr_intercept": sqltype.RegressionIntercept, "regr_r2": sqltype.RegressionR2,
}

func regressionAggregateName(name string) bool {
	_, ok := regressionFinals[name]
	return ok || name == "regr_count"
}

func bindStatisticalAggregate(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, error) {
	var small [2]Argument
	var names [2]string
	var operands []Argument
	var types []string
	if len(raw.Args) <= 2 {
		operands, types = small[:len(raw.Args)], names[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
		types = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		o, err := comparisonOperand(b, child)
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = o
			types[i] = b.DiagnosticTypeName(o.Usage.Type)
		}
	}
	filter, err := b.AnalyzeAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	args, usage, err := resolveStatisticalSignature(b, raw, name, operands, types)
	if err != nil {
		return nil, err
	}
	var provider AggregateProvider
	var operation AggregateOperation
	if raw.Name == "regr_count" {
		operation = AggregateRegrCount
		provider = NewRegressionCountAggregate(b.Type(sqltype.Float8), usage)
	} else if final, ok := regressionFinals[raw.Name]; ok {
		// The operation constants follow sqltype's regression final order.
		operation = AggregateRegrSlope + AggregateOperation(final)
		provider = NewRegressionAggregate(b.Type(sqltype.Float8), usage, final)
	} else {
		sample := raw.Name != "var_pop" && raw.Name != "stddev_pop"
		stddev := raw.Name == "stddev" || raw.Name == "stddev_samp" || raw.Name == "stddev_pop"
		operation = AggregateVarPop
		if sample {
			operation = AggregateVarSamp
		}
		if stddev {
			if sample {
				operation = AggregateStddevSamp
			} else {
				operation = AggregateStddevPop
			}
		}
		provider = NewStatisticalAggregate(args[0].ResultUsage().Type, usage, sample, stddev)
	}
	call := AggregateCall{Provider: provider, Argument: args[0], Separator: args[1], Operation: operation}
	formals := [2]sqltype.ID{args[0].ResultUsage().Type.ID()}
	arity := 1
	if regressionAggregateName(raw.Name) {
		formals[1], arity = sqltype.Float8, 2
	}
	call.Routine, err = b.BuiltinRoutineReference(AggregateRoutineProvider, raw.Name, formals[:arity]...)
	if err != nil {
		return nil, err
	}
	return b.FinishAggregate(raw, call, filter, b.Info(usage, raw.Pos, expr.Properties{}))
}

// resolveStatisticalSignature resolves and coerces the callable; window
// calls share it without constructing group state.
func resolveStatisticalSignature(b Binder, raw parser.AggregateExpr, name parser.ObjectName, operands []Argument, types []string) ([2]expr.Expr, sqltype.Usage, error) {
	var args [2]expr.Expr
	if len(raw.Args) > 100 {
		return args, sqltype.Usage{}, aggregateLimitError(raw.Pos)
	}
	visible, err := b.BuiltinVisible(name)
	if err != nil {
		return args, sqltype.Usage{}, err
	}
	arity := 1
	if regressionAggregateName(raw.Name) {
		arity = 2
	}
	if !visible || raw.Star || len(operands) != arity {
		return args, sqltype.Usage{}, ResolutionError(name, raw.Pos, types, false)
	}
	result := b.Type(sqltype.Float8)
	if raw.Name == "regr_count" {
		result = b.Type(sqltype.Int8)
	}
	for i, o := range operands {
		typ := o.Usage.Type
		if arity == 2 {
			target := b.Type(sqltype.Float8).Usage()
			if typ != nil {
				if _, err := sqltype.PlanCoercion(o.Usage, target, sqltype.Implicit); err != nil {
					return args, sqltype.Usage{}, ResolutionError(name, raw.Pos, types, false)
				}
			}
			args[i], err = coerceConditional(b, o, target, raw.Name)
		} else {
			// FLOAT8 is the preferred numeric-category overload for unknowns.
			if typ == nil {
				typ = b.Type(sqltype.Float8)
			}
			switch typ {
			case b.Type(sqltype.Int2), b.Type(sqltype.Int4), b.Type(sqltype.Int8), b.Type(sqltype.Numeric):
				result = b.Type(sqltype.Numeric)
			case b.Type(sqltype.Float4), b.Type(sqltype.Float8):
			default:
				return args, sqltype.Usage{}, ResolutionError(name, raw.Pos, types, false)
			}
			args[i], err = finalizeOperand(b, o, typ)
		}
		if err != nil {
			return args, sqltype.Usage{}, err
		}
	}
	return args, result.Usage(), nil
}

func windowStatisticalAggregate(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, sqltype.Usage, error) {
	operands, types, err := windowAggregateOperands(b, raw, plainTypeName)
	if err != nil {
		return nil, sqltype.Usage{}, err
	}
	args, usage, err := resolveStatisticalSignature(b, raw, name, operands, types)
	return args[0], usage, err
}
