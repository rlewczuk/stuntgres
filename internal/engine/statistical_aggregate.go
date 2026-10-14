package engine

import (
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func statisticalAggregateName(name string) bool {
	switch name {
	case "var_pop", "var_samp", "variance", "stddev_pop", "stddev_samp", "stddev", "regr_slope", "regr_sxx", "regr_sxy", "regr_syy":
		return true
	}
	return false
}
func regressionAggregateName(name string) bool {
	return name == "regr_slope" || name == "regr_sxx" || name == "regr_sxy" || name == "regr_syy"
}

func (b *expressionBinder) bindStatisticalAggregate(raw parser.AggregateExpr, name parser.ObjectName) (BoundExpr, error) {
	var small [2]comparisonOperand
	var names [2]string
	var operands []comparisonOperand
	var types []string
	if len(raw.Args) <= 2 {
		operands, types = small[:len(raw.Args)], names[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		operands = make([]comparisonOperand, len(raw.Args))
		types = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		o, err := b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = o
			types[i] = b.diagnosticTypeName(o.descriptor())
		}
	}
	filter, err := b.bindAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	args, usage, err := b.resolveStatisticalSignature(raw, name, operands, types)
	if err != nil {
		return nil, err
	}
	var provider aggregateProvider
	var operation aggregateOperation
	if regressionAggregateName(raw.Name) {
		final := sqltype.RegressionSlope
		switch raw.Name {
		case "regr_sxx":
			final = sqltype.RegressionSxx
		case "regr_sxy":
			final = sqltype.RegressionSxy
		case "regr_syy":
			final = sqltype.RegressionSyy
		}
		operation = aggregateRegrSlope + aggregateOperation(final)
		provider = functions.NewRegressionAggregate(b.typ(sqltype.Float8), usage, final)
	} else {
		sample := raw.Name != "var_pop" && raw.Name != "stddev_pop"
		stddev := raw.Name == "stddev" || raw.Name == "stddev_samp" || raw.Name == "stddev_pop"
		operation = aggregateVarPop
		if sample {
			operation = aggregateVarSamp
		}
		if stddev {
			if sample {
				operation = aggregateStddevSamp
			} else {
				operation = aggregateStddevPop
			}
		}
		provider = functions.NewStatisticalAggregate(args[0].ResultUsage().Type, usage, sample, stddev)
	}
	call := aggregateCall{provider: provider, argument: args[0], separator: args[1], operation: operation}
	return b.finishAggregate(raw, call, filter, b.info(usage, raw.Pos, ExprProperties{}))
}

// Window exclusion reuses signature analysis without constructing group state.
func (b *expressionBinder) resolveStatisticalSignature(raw parser.AggregateExpr, name parser.ObjectName, operands []comparisonOperand, types []string) ([2]BoundExpr, sqltype.Usage, error) {
	var args [2]BoundExpr
	if len(raw.Args) > 100 {
		return args, sqltype.Usage{}, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(name, functionLookup)
	if err != nil {
		return args, sqltype.Usage{}, err
	}
	arity := 1
	if regressionAggregateName(raw.Name) {
		arity = 2
	}
	if !visible || raw.Star || len(operands) != arity {
		return args, sqltype.Usage{}, functionSignatureErrorForTypes(name, raw.Pos, types)
	}
	result := b.typ(sqltype.Float8)
	for i, o := range operands {
		typ := o.descriptor()
		if arity == 2 {
			target := b.typ(sqltype.Float8).Usage()
			if typ != nil {
				if _, err := sqltype.PlanCoercion(o.node.ResultUsage(), target, sqltype.Implicit); err != nil {
					return args, sqltype.Usage{}, functionSignatureErrorForTypes(name, raw.Pos, types)
				}
			}
			args[i], err = b.coerceConditional(o, target, raw.Name)
		} else {
			// FLOAT8 is the preferred numeric-category overload for unknowns.
			if typ == nil {
				typ = b.typ(sqltype.Float8)
			}
			switch typ {
			case b.typ(sqltype.Int2), b.typ(sqltype.Int4), b.typ(sqltype.Int8), b.typ(sqltype.Numeric):
				result = b.typ(sqltype.Numeric)
			case b.typ(sqltype.Float4), b.typ(sqltype.Float8):
			default:
				return args, sqltype.Usage{}, functionSignatureErrorForTypes(name, raw.Pos, types)
			}
			args[i], err = b.finalizeOperand(o, typ)
		}
		if err != nil {
			return args, sqltype.Usage{}, err
		}
	}
	return args, result.Usage(), nil
}
