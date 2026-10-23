package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// HypotheticalSetName reports the rank family, which are window functions
// and, with WITHIN GROUP, hypothetical-set aggregates.
func HypotheticalSetName(name string) bool {
	return name == "rank" || name == "dense_rank" || name == "percent_rank" || name == "cume_dist"
}

// OrderedSetName reports the WITHIN GROUP aggregates.
func OrderedSetName(name string) bool {
	return name == "percentile_cont" || name == "percentile_disc" || name == "mode" || HypotheticalSetName(name)
}

// Hypothetical reports the hypothetical-set aggregate identities.
func (op AggregateOperation) Hypothetical() bool {
	return op >= AggregateHypotheticalRank && op <= AggregateHypotheticalCumeDist
}

// withinGroupOperands analyzes the direct arguments followed by the WITHIN
// GROUP keys, spelling their types as min and max do.
func withinGroupOperands(b AggregateBinder, raw parser.AggregateExpr, operands []Argument, types []string) error {
	for i := range operands {
		var child parser.Expr
		if i < len(raw.Args) {
			child = raw.Args[i]
		} else {
			child = raw.Order[i-len(raw.Args)].Expr
		}
		var err error
		operands[i], err = comparisonOperand(b, child)
		if err != nil {
			return err
		}
		types[i], err = b.DomainTypeName(operands[i].Usage.Type)
		if err != nil {
			return err
		}
	}
	return nil
}

// implicitCollationMismatch is the 42P21 for an indeterminate collation.
func implicitCollationMismatch(b AggregateBinder, left, right sqltype.CollationRef, position parser.Position) error {
	d := expr.ErrorAt("42P21", fmt.Sprintf(`collation mismatch between implicit collations "%s" and "%s"`, b.CollationName(left.ID), b.CollationName(right.ID)), position).(*sqlstate.Error)
	d.Hint = "You can choose the collation by applying the COLLATE clause to one or both expressions."
	return d
}

func bindOrderedSet(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, error) {
	if HypotheticalSetName(raw.Name) {
		return bindHypotheticalSet(b, raw, name)
	}
	var localOperands [2]Argument
	var localTypes [2]string
	n := len(raw.Args)
	if raw.Within {
		n += len(raw.Order)
	}
	operands, types := localOperands[:], localTypes[:]
	if n > 2 {
		operands = make([]Argument, n)
		types = make([]string, n)
	} else {
		operands, types = operands[:n], types[:n]
	}
	if err := withinGroupOperands(b, raw, operands, types); err != nil {
		return nil, err
	}
	filter, err := b.AnalyzeAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(name)
	if err != nil {
		return nil, err
	}
	directCount := 1
	operation := AggregatePercentileCont
	if raw.Name == "mode" {
		directCount = 0
		operation = AggregateMode
	} else if raw.Name == "percentile_disc" {
		operation = AggregatePercentileDisc
	}
	undefined := func() (expr.Expr, error) { return nil, UnnotedResolutionError(b, name, raw.Pos, types, false) }
	if !visible || n != directCount+1 {
		return undefined()
	}
	inputOperand := operands[n-1]
	inputType := inputOperand.Usage.Type
	array := false
	var direct []expr.Expr
	fraction := b.Type(sqltype.Float8)
	if directCount == 1 {
		fractionType := fraction
		if typ := operands[0].Usage.Type; typ != nil && typ.BaseType().IsArray() {
			array = true
			fractionType = b.Type(fraction.AssociatedArray())
		}
		if typ := operands[0].Usage.Type; typ != nil {
			if _, err := sqltype.PlanCoercion(typ.Usage(), fractionType.Usage(), sqltype.Implicit); err != nil {
				return undefined()
			}
		}
		direct = make([]expr.Expr, 1)
		direct[0], err = coerceConditional(b, operands[0], fractionType.Usage(), raw.Name)
		if err != nil {
			return nil, err
		}
	}
	if operation == AggregatePercentileCont {
		selected := fraction
		if inputType != nil && inputType.BaseType().ID() == sqltype.Interval {
			selected = b.Type(sqltype.Interval)
		}
		if inputType != nil {
			if _, err := sqltype.PlanCoercion(inputType.Usage(), selected.Usage(), sqltype.Implicit); err != nil {
				return undefined()
			}
		}
		inputType = selected
	} else if inputType == nil {
		inputType = b.Type(sqltype.Text)
	}
	if !raw.Within {
		return nil, expr.ErrorAt("42809", "WITHIN GROUP is required for ordered-set aggregate "+raw.Name, raw.Pos)
	}
	if raw.Window != nil {
		return nil, expr.ErrorAt("0A000", "OVER is not supported for ordered-set aggregate "+raw.Name, raw.Pos)
	}
	if len(raw.Args) != directCount {
		d := ResolutionError(name, raw.Pos, types, false).(*sqlstate.Error)
		d.Hint = fmt.Sprintf("There is an ordered-set aggregate %s, but it requires %d direct argument, not %d.", raw.Name, directCount, len(raw.Args))
		return nil, d
	}
	if len(raw.Order) != 1 {
		return undefined()
	}
	arg, err := coerceConditional(b, inputOperand, inputType.Usage(), raw.Name)
	if err != nil {
		return nil, err
	}
	usage := arg.ResultUsage()
	usage.Typmod = -1
	if arg.CollationIndeterminate() {
		left, right, position := arg.ImplicitCollationConflict()
		return nil, implicitCollationMismatch(b, left, right, position)
	}
	comparison, err := AggregateOrdering(arg.ResultUsage(), raw.Order[0].Pos, false)
	if err != nil {
		return nil, err
	}
	if array {
		result, ok := b.ArrayType(usage.Type)
		if !ok || !result.Concrete() {
			return nil, sqlError("42704", "could not find array type for data type "+b.DiagnosticTypeName(usage.Type))
		}
		usage.Type = result
	}
	provider, err := NewOrderedSet(usage, inputType, fraction, operation, array, comparison)
	if err != nil {
		return nil, err
	}
	call := AggregateCall{Provider: provider, Arguments: []expr.Expr{arg}, Direct: direct, Operation: operation, StageWidth: 2,
		Order: []AggregateSortKey{{Expression: arg, Comparison: comparison, Key: raw.Order[0], Argument: true}}}
	var formal [2]sqltype.ID
	formalCount := 2
	if operation == AggregateMode {
		formal[0], formalCount = sqltype.ID(2283), 1
	} else {
		formal[0] = sqltype.Float8
		if array {
			formal[0] = sqltype.ID(1022)
		}
		formal[1] = sqltype.ID(2283)
		if operation == AggregatePercentileCont {
			formal[1] = inputType.ID()
		}
	}
	call.Routine, err = b.BuiltinRoutineReference(AggregateRoutineProvider, raw.Name, formal[:formalCount]...)
	if err != nil {
		return nil, err
	}
	info := b.Info(usage, raw.Pos, expr.Properties{})
	info.CollationConflict, info.CollationExplicit = arg.CollationIndeterminate(), arg.ExplicitCollation()
	return b.FinishAggregate(raw, call, filter, info)
}

func bindHypotheticalSet(b AggregateBinder, raw parser.AggregateExpr, name parser.ObjectName) (expr.Expr, error) {
	n := len(raw.Args)
	if raw.Within {
		n += len(raw.Order)
	}
	operands := make([]Argument, n)
	types := make([]string, n)
	if err := withinGroupOperands(b, raw, operands, types); err != nil {
		return nil, err
	}
	filter, err := b.AnalyzeAggregateFilter(raw)
	if err != nil {
		return nil, err
	}
	if n > 100 {
		return nil, aggregateLimitError(raw.Pos)
	}
	visible, err := b.BuiltinVisible(name)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, UnnotedResolutionError(b, name, raw.Pos, types, false)
	}
	if !raw.Within {
		return nil, expr.ErrorAt("42809", "WITHIN GROUP is required for ordered-set aggregate "+qualifiedName(name), raw.Pos)
	}
	if raw.Window != nil {
		return nil, expr.ErrorAt("0A000", "OVER is not supported for ordered-set aggregate "+qualifiedName(name), raw.Pos)
	}
	if len(raw.Args) != len(raw.Order) || len(raw.Args) == 0 {
		d := ResolutionError(name, raw.Pos, types, false).(*sqlstate.Error)
		d.Hint = fmt.Sprintf("To use the hypothetical-set aggregate %s, the number of hypothetical direct arguments (here %d) must match the number of ordering columns (here %d).", raw.Name, len(raw.Args), len(raw.Order))
		return nil, d
	}
	// Aggregates take one argument fewer than other functions; PostgreSQL
	// 18.6 checks it once a variadic hypothetical-set call resolves.
	if n > 99 {
		return nil, expr.ErrorAt("54023", "aggregates cannot have more than 99 arguments", raw.Pos)
	}
	op := AggregateHypotheticalRank
	result := b.Type(sqltype.Int8)
	switch raw.Name {
	case "dense_rank":
		op = AggregateHypotheticalDenseRank
	case "percent_rank":
		op, result = AggregateHypotheticalPercentRank, b.Type(sqltype.Float8)
	case "cume_dist":
		op, result = AggregateHypotheticalCumeDist, b.Type(sqltype.Float8)
	}
	call := AggregateCall{Operation: op, IncludeNulls: true, Direct: make([]expr.Expr, len(raw.Args)), Order: make([]AggregateSortKey, len(raw.Order)), StageWidth: 2 + len(raw.Order)}
	// Signature transformation coerces every pair before sort operators or
	// pair collations are examined. A later type error must win over either.
	// The checked 99-argument cap bounds this stack scratch to 49 pairs.
	var typmods [49]int32
	for i, key := range raw.Order {
		pair := [2]Argument{operands[len(raw.Args)+i], operands[i]}
		usage, err := b.CommonArgumentUsage(pair[:], "WITHIN GROUP")
		if err != nil {
			return nil, err
		}
		input, err := coerceConditional(b, pair[0], usage, "WITHIN GROUP")
		if err != nil {
			return nil, err
		}
		direct, err := coerceConditional(b, pair[1], usage, "WITHIN GROUP")
		if err != nil {
			return nil, err
		}
		typmods[i] = usage.Typmod
		call.Direct[i] = direct
		call.Order[i] = AggregateSortKey{Expression: input, Key: key}
	}
	// Aggregate sort-list construction resolves every ordering operator before
	// collation assignment. Use each coerced input's provisional collation;
	// this is capability binding only, not runtime comparison or evaluation.
	for i, key := range call.Order {
		comparison, err := AggregateOrdering(key.Expression.ResultUsage(), raw.Order[i].Pos, false)
		if err != nil {
			return nil, err
		}
		call.Order[i].Comparison = comparison
	}
	for i, key := range call.Order {
		input, direct := key.Expression, call.Direct[i]
		// Pair collation authority is direct-first, unlike common-type authority.
		info, err := b.ComparisonInfo(direct, input)
		if err != nil {
			return nil, err
		}
		if info.CollationConflict {
			return nil, implicitCollationMismatch(b, info.CollationLeft, info.CollationRight, info.CollationConflictPos)
		}
		usage := input.ResultUsage()
		usage.Typmod, usage.Collation = typmods[i], info.Usage.Collation
		if usage.Collation != input.ResultUsage().Collation {
			comparison, err := AggregateOrdering(usage, raw.Order[i].Pos, false)
			if err != nil {
				return nil, err
			}
			call.Order[i].Comparison = comparison
		}
		// Common typmod is either the operands' identical typmod or -1. This
		// metadata wrapper drops a restriction without recasting stored values.
		wrap := func(child expr.Expr) expr.Expr {
			metadata := info
			metadata.Usage, metadata.Pos, metadata.Property = usage, child.SourcePosition(), child.Properties()
			return b.CollationRelabel(child, metadata)
		}
		call.Order[i].Expression, call.Direct[i] = wrap(input), wrap(direct)
	}
	usage := result.Usage()
	call.Provider = NewHypotheticalSet(usage, op)
	// The released variadic ANY formal owns the whole direct/order list;
	// neither its expanded arity nor the coerced pair types change identity.
	call.Routine, err = b.BuiltinRoutineReference(AggregateRoutineProvider, raw.Name, sqltype.ID(2276))
	if err != nil {
		return nil, err
	}
	return b.FinishAggregate(raw, call, filter, b.Info(usage, raw.Pos, expr.Properties{}))
}
