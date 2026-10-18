package engine

import (
	"fmt"
	"stuntgres/internal/engine/functions"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func hypotheticalSetName(name string) bool {
	return name == "rank" || name == "dense_rank" || name == "percent_rank" || name == "cume_dist"
}

func hypotheticalOperation(op functions.AggregateOperation) bool {
	return op >= functions.AggregateHypotheticalRank && op <= functions.AggregateHypotheticalCumeDist
}

func (b *expressionBinder) bindHypotheticalSet(raw parser.AggregateExpr, name parser.ObjectName) (BoundExpr, error) {
	n := len(raw.Args)
	if raw.Within {
		n += len(raw.Order)
	}
	operands := make([]comparisonOperand, n)
	types := make([]string, n)
	for i := range operands {
		var child parser.Expr
		if i < len(raw.Args) {
			child = raw.Args[i]
		} else {
			child = raw.Order[i-len(raw.Args)].Expr
		}
		var err error
		operands[i], err = b.comparisonOperand(child)
		if err != nil {
			return nil, err
		}
		types[i], err = b.extremaArgumentTypeName(operands[i])
		if err != nil {
			return nil, err
		}
	}
	filter, err := b.bindAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	if n > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	visible, err := b.builtinVisible(name, functionLookup)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, functionSignatureErrorForTypes(name, raw.Pos, types)
	}
	if !raw.Within {
		return nil, expressionError("42809", "WITHIN GROUP is required for ordered-set aggregate "+objectNameText(name), raw.Pos)
	}
	if raw.Window != nil {
		return nil, expressionError("0A000", "OVER is not supported for ordered-set aggregate "+objectNameText(name), raw.Pos)
	}
	if len(raw.Args) != len(raw.Order) || len(raw.Args) == 0 {
		d := functionSignatureErrorForTypes(name, raw.Pos, types).(*sqlstate.Error)
		d.Hint = fmt.Sprintf("To use the hypothetical-set aggregate %s, the number of hypothetical direct arguments (here %d) must match the number of ordering columns (here %d).", raw.Name, len(raw.Args), len(raw.Order))
		return nil, d
	}
	op := functions.AggregateHypotheticalRank
	result := b.typ(sqltype.Int8)
	switch raw.Name {
	case "dense_rank":
		op = functions.AggregateHypotheticalDenseRank
	case "percent_rank":
		op, result = functions.AggregateHypotheticalPercentRank, b.typ(sqltype.Float8)
	case "cume_dist":
		op, result = functions.AggregateHypotheticalCumeDist, b.typ(sqltype.Float8)
	}
	call := aggregateCall{operation: op, includeNulls: true, direct: make([]BoundExpr, len(raw.Args)), order: make([]sortKey, len(raw.Order)), stageWidth: 2 + len(raw.Order)}
	// Signature transformation coerces every pair before sort operators or
	// pair collations are examined. A later type error must win over either.
	// The checked 100-operand cap bounds this stack scratch to 50 pairs.
	var typmods [50]int32
	for i, key := range raw.Order {
		pair := [2]comparisonOperand{operands[len(raw.Args)+i], operands[i]}
		usage, err := commonExpressionUsage(pair[:], b.typ(sqltype.Text).Usage(), "WITHIN GROUP")
		if err != nil {
			return nil, err
		}
		input, err := b.coerceConditional(pair[0], usage, "WITHIN GROUP")
		if err != nil {
			return nil, err
		}
		direct, err := b.coerceConditional(pair[1], usage, "WITHIN GROUP")
		if err != nil {
			return nil, err
		}
		typmods[i] = usage.Typmod
		call.direct[i] = direct
		call.order[i] = sortKey{expression: input, position: key.Pos.Character, desc: key.Desc, nullsFirst: key.Nulls == parser.NullsFirst || key.Nulls == parser.NullsDefault && key.Desc, slot: -1, cache: 2 + i}
	}
	// Aggregate sort-list construction resolves every ordering operator before
	// collation assignment. Use each coerced input's provisional collation;
	// this is capability binding only, not runtime comparison or evaluation.
	for i, key := range call.order {
		comparison, err := aggregateOrdering(key.expression.ResultUsage(), raw.Order[i].Pos, false)
		if err != nil {
			return nil, err
		}
		call.order[i].comparison = comparison
	}
	for i, key := range call.order {
		input, direct := key.expression, call.direct[i]
		// Pair collation authority is direct-first, unlike common-type authority.
		info, err := b.comparisonInfo(direct, input)
		if err != nil {
			return nil, err
		}
		if info.CollationConflict {
			d := expressionError("42P21", fmt.Sprintf(`collation mismatch between implicit collations "%s" and "%s"`, b.collationName(info.CollationLeft.ID), b.collationName(info.CollationRight.ID)), info.CollationConflictPos).(*sqlstate.Error)
			d.Hint = "You can choose the collation by applying the COLLATE clause to one or both expressions."
			return nil, d
		}
		usage := input.ResultUsage()
		usage.Typmod, usage.Collation = typmods[i], info.Usage.Collation
		if usage.Collation != input.ResultUsage().Collation {
			comparison, err := aggregateOrdering(usage, raw.Order[i].Pos, false)
			if err != nil {
				return nil, err
			}
			call.order[i].comparison = comparison
		}
		// Common typmod is either the operands' identical typmod or -1. This
		// metadata wrapper drops a restriction without recasting stored values.
		wrap := func(child BoundExpr) BoundExpr {
			metadata := info
			metadata.Usage, metadata.Pos, metadata.Property = usage, child.SourcePosition(), child.Properties()
			return collationExpression{expressionInfo: metadata, child: child}
		}
		call.order[i].expression, call.direct[i] = wrap(input), wrap(direct)
	}
	usage := result.Usage()
	call.provider = &hypotheticalSetProvider{usage: usage, operation: op}
	// The released variadic ANY formal owns the whole direct/order list;
	// neither its expanded arity nor the coerced pair types change identity.
	call.routine, err = b.builtinRoutineReference(aggregateRoutineProvider, raw.Name, sqltype.ID(2276))
	if err != nil {
		return nil, err
	}
	return b.finishAggregate(raw, call, filter, b.info(usage, raw.Pos, ExprProperties{}))
}
