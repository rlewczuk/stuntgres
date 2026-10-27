package plan

import (
	"context"
	"math"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// ColumnStatisticInput is detached authority captured by the caller. Column
// identity includes origin/frame/occurrence, not a display name or row slot alone.
type ColumnStatisticInput struct {
	Column     ColumnExpr
	Statistics *storage.ColumnStatistics
}

type selectivityTruth struct {
	yes, no float64
	known   bool
}

func selectivityUnit(value float64) float64 {
	if math.IsNaN(value) || value < 0 {
		return 0
	}
	return min(value, 1)
}

// EstimateSelectivity never evaluates expressions, accesses the engine, or reads
// parameters. Equality/NULL/retained MCV mass are exact capture facts; residual
// uniformity, histogram rank CDF and Boolean independence are approximations.
// Unavailable authority returns known=false, preserving the existing fallback.
func EstimateSelectivity(ctx context.Context, predicate expr.Expr, statistics []ColumnStatisticInput) (float64, bool, error) {
	result, err := estimateStatisticsTruth(ctx, predicate, statistics)
	return selectivityUnit(result.yes), result.known, err
}

func statisticForColumn(expression expr.Expr, inputs []ColumnStatisticInput) (*storage.ColumnStatistics, bool) {
	column, ok := expression.(ColumnExpr)
	if !ok || column.Virtual != nil || column.MergeSource {
		return nil, false
	}
	for _, input := range inputs {
		c, s := input.Column, input.Statistics
		if s != nil && s.Rows != 0 && column.Origin == c.Origin && column.Scope == c.Scope && column.Frame == c.Frame && column.Slot == c.Slot && column.QueryOccurrence == c.QueryOccurrence && column.Usage == c.Usage && s.Usage == column.Usage {
			return s, true
		}
	}
	return nil, false
}

func estimateStatisticsTruth(ctx context.Context, predicate expr.Expr, inputs []ColumnStatisticInput) (selectivityTruth, error) {
	if err := ctx.Err(); err != nil {
		return selectivityTruth{}, err
	}
	switch p := predicate.(type) {
	case LiteralExpr:
		truth, err := TruthOf(p.Value)
		if err != nil {
			return selectivityTruth{}, nil
		}
		r := selectivityTruth{known: true}
		if truth == True {
			r.yes = 1
		} else if truth == False {
			r.no = 1
		}
		return r, nil
	case NullTestExpr:
		s, ok := statisticForColumn(p.Child, inputs)
		if !ok || (!p.Whole && s.Usage.Type.BaseType().IsComposite()) {
			return selectivityTruth{}, nil
		}
		yes := s.NullFraction()
		if p.Not {
			yes = 1 - yes
		}
		return selectivityTruth{yes: yes, no: 1 - yes, known: true}, nil
	case LogicalExpr:
		if p.QueryRight {
			return selectivityTruth{}, nil
		}
		a, err := estimateStatisticsTruth(ctx, p.Left, inputs)
		if err != nil || !a.known {
			return selectivityTruth{}, err
		}
		if p.Op == parser.LogicalNot {
			return selectivityTruth{yes: a.no, no: a.yes, known: true}, nil
		}
		b, err := estimateStatisticsTruth(ctx, p.Right, inputs)
		if err != nil || !b.known {
			return selectivityTruth{}, err
		}
		r := selectivityTruth{known: true}
		if p.Op == parser.LogicalAnd {
			r.yes, r.no = a.yes*b.yes, a.no+b.no-a.no*b.no
		} else if p.Op == parser.LogicalOr {
			r.yes, r.no = a.yes+b.yes-a.yes*b.yes, a.no*b.no
		} else {
			return selectivityTruth{}, nil
		}
		return r, nil
	case ComparisonExpr:
		return estimateStatisticsComparison(ctx, p, inputs)
	}
	return selectivityTruth{}, nil
}

func estimateStatisticsComparison(ctx context.Context, p ComparisonExpr, inputs []ColumnStatisticInput) (selectivityTruth, error) {
	if p.Namespace != "" && p.Namespace != "pg_catalog" {
		return selectivityTruth{}, nil
	}
	s, ok := statisticForColumn(p.Left, inputs)
	literal, literalOK := p.Right.(LiteralExpr)
	reversed := false
	if !ok || !literalOK {
		s, ok = statisticForColumn(p.Right, inputs)
		literal, literalOK = p.Left.(LiteralExpr)
		reversed = true
	}
	if !ok || !literalOK || literal.Value.Type() != s.Usage.Type {
		return selectivityTruth{}, nil
	}
	var operator sqltype.ComparisonOperator
	switch p.Operator {
	case "=":
		operator = sqltype.CompareEqual
	case "<>", "!=":
		operator = sqltype.CompareNotEqual
	case "<":
		operator = sqltype.CompareLess
	case "<=":
		operator = sqltype.CompareLessEqual
	case ">":
		operator = sqltype.CompareGreater
	case ">=":
		operator = sqltype.CompareGreaterEqual
	default:
		return selectivityTruth{}, nil
	}
	// Prove the bound predicate is the supported built-in strict typed operator.
	expected, err := sqltype.BindComparisonPredicate(operator, s.Usage.Type, s.Usage.Type, p.Usage.Type, s.Usage.Collation, false)
	if err != nil || !expected.Equal(p.Predicate) {
		return selectivityTruth{}, nil
	}
	if literal.Value.IsNull() {
		return selectivityTruth{known: true}, nil
	}
	if reversed {
		switch operator {
		case sqltype.CompareLess:
			operator = sqltype.CompareGreater
		case sqltype.CompareLessEqual:
			operator = sqltype.CompareGreaterEqual
		case sqltype.CompareGreater:
			operator = sqltype.CompareLess
		case sqltype.CompareGreaterEqual:
			operator = sqltype.CompareLessEqual
		}
	}
	// Keep exact counts through MCV summation and complements. Subtracting
	// rounded frequencies can invent residual mass in a fully covered column
	// and inflate an exact integer result when EXPLAIN rounds upward.
	nonNull := s.Rows - s.NullCount
	var yes float64
	if operator == sqltype.CompareEqual || operator == sqltype.CompareNotEqual {
		if !s.DistinctKnown {
			return selectivityTruth{}, nil
		}
		grouping, err := sqltype.BindGrouping(s.Usage)
		if err != nil {
			return selectivityTruth{}, err
		}
		key, err := grouping.PrepareKey(ctx, literal.Value)
		if err != nil {
			return selectivityTruth{}, err
		}
		var mass uint64
		hit := false
		for _, mcv := range s.MCV {
			equal, err := grouping.Equal(ctx, mcv.Value, key)
			if err != nil {
				return selectivityTruth{}, err
			}
			mass += mcv.Count
			if equal {
				yes, hit = float64(mcv.Count), true
			}
		}
		if !hit {
			remaining := float64(s.DistinctCount) - float64(len(s.MCV))
			yes = float64(nonNull-mass) / max(1, remaining)
		}
		if operator == sqltype.CompareNotEqual {
			yes = max(0, float64(nonNull)-yes)
		}
	} else {
		ordering, err := sqltype.BindOrdering(s.Usage)
		if err != nil {
			if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "42883" {
				return selectivityTruth{}, nil
			}
			return selectivityTruth{}, err
		}
		qualifies := func(order int) bool {
			switch operator {
			case sqltype.CompareLess:
				return order < 0
			case sqltype.CompareLessEqual:
				return order <= 0
			case sqltype.CompareGreater:
				return order > 0
			default:
				return order >= 0
			}
		}
		var mass uint64
		for _, mcv := range s.MCV {
			order, err := ordering.Compare(ctx, mcv.Value, literal.Value, sqltype.InputEnvironment{})
			if err != nil {
				return selectivityTruth{}, err
			}
			mass += mcv.Count
			if qualifies(order) {
				yes += float64(mcv.Count)
			}
		}
		residual := nonNull - mass
		if residual > 0 {
			if len(s.Histogram) == 0 {
				return selectivityTruth{}, nil
			}
			// Rank CDF: weighted quantile boundaries are equal-mass witnesses.
			// This needs no formatted order or unsafe numeric conversion, handles
			// duplicate/singleton endpoints, and is explicitly approximate.
			matching := 0
			for _, boundary := range s.Histogram {
				order, err := ordering.Compare(ctx, boundary, literal.Value, sqltype.InputEnvironment{})
				if err != nil {
					return selectivityTruth{}, err
				}
				if qualifies(order) {
					matching++
				}
			}
			yes += float64(residual) * float64(matching) / float64(len(s.Histogram))
		}
	}
	yes = min(max(yes, 0), float64(nonNull))
	return selectivityTruth{
		yes:   selectivityUnit(yes / float64(s.Rows)),
		no:    selectivityUnit((float64(nonNull) - yes) / float64(s.Rows)),
		known: true,
	}, ctx.Err()
}
