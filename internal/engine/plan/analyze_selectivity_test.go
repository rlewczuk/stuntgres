package plan

import (
	"context"
	"errors"
	"math"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestAnalyzeEstimateSelectivity(t *testing.T) {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	integer, boolean := types[sqltype.Int4], types[sqltype.Bool]
	value := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(integer, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	column := ColumnExpr{Info: expr.Info{Usage: integer.Usage()}, Scope: 1, Frame: 2, Slot: 0, QueryOccurrence: 1}
	stats := &storage.ColumnStatistics{Usage: integer.Usage(), Rows: 20, NullCount: 4, DistinctKnown: true, DistinctCount: 2, MCV: []storage.StatisticMCV{{Value: value(1), Count: 14}, {Value: value(2), Count: 2}}}
	inputs := []ColumnStatisticInput{{Column: column, Statistics: stats, SourceRows: 20}}
	comparison := func(op string, operator sqltype.ComparisonOperator, v sqltype.Value) ComparisonExpr {
		predicate, err := sqltype.BindComparisonPredicate(operator, integer, integer, boolean, integer.Usage().Collation, false)
		if err != nil {
			t.Fatal(err)
		}
		return ComparisonExpr{Info: expr.Info{Usage: boolean.Usage()}, Left: column, Right: LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: v}, Operator: op, Predicate: predicate}
	}
	assert := func(name string, predicate expr.Expr, want float64, known bool) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			got, ok, err := EstimateSelectivity(context.Background(), predicate, inputs)
			if err != nil || ok != known || math.Abs(got-want) > 1e-12 || math.IsNaN(got) || math.IsInf(got, 0) || got < 0 || got > 1 {
				t.Fatalf("selectivity %v known=%v err=%v want=%v/%v", got, ok, err, want, known)
			}
		})
	}
	common := comparison("=", sqltype.CompareEqual, value(1))
	rare := comparison("=", sqltype.CompareEqual, value(2))
	assert("common exact", common, .7, true)
	assert("rare exact", rare, .1, true)
	assert("unseen covered population", comparison("=", sqltype.CompareEqual, value(3)), 0, true)
	assert("strict null", comparison("=", sqltype.CompareEqual, sqltype.Null(integer)), 0, true)
	assert("not equal excludes null", comparison("<>", sqltype.CompareNotEqual, value(1)), .1, true)
	assert("is null", NullTestExpr{Child: column}, .2, true)
	assert("is not null", NullTestExpr{Child: column, Not: true}, .8, true)
	assert("range MCV strict", comparison("<", sqltype.CompareLess, value(2)), .7, true)
	assert("range MCV inclusive", comparison("<=", sqltype.CompareLessEqual, value(2)), .8, true)
	assert("not three valued", LogicalExpr{Op: parser.LogicalNot, Left: common}, .1, true)
	assert("and independent", LogicalExpr{Op: parser.LogicalAnd, Left: common, Right: rare}, .07, true)
	assert("or independent", LogicalExpr{Op: parser.LogicalOr, Left: common, Right: rare}, .73, true)
	reversed := comparison(">", sqltype.CompareGreater, value(2))
	reversed.Left, reversed.Right = reversed.Right, reversed.Left
	assert("literal first reversed range", reversed, .7, true)
	other := column
	other.QueryOccurrence++
	wrong := common
	wrong.Left = other
	assert("self join occurrence isolated", wrong, 0, false)
	wrong = common
	wrong.Predicate = sqltype.ComparisonPredicate{}
	assert("unbound predicate rejected", wrong, 0, false)
	wrong = common
	wrong.Namespace = "custom"
	assert("custom operator fallback", wrong, 0, false)
	assert("parameter never evaluated", ParameterExpr{Info: expr.Info{Usage: integer.Usage()}, Number: 1}, 0, false)
	assert("function never evaluated", selectivityUnevaluable{}, 0, false)
	assert("literal true", LiteralExpr{Value: sqltype.Boolean(boolean, true)}, 1, true)
	assert("literal null", LiteralExpr{Value: sqltype.Null(boolean)}, 0, true)

	t.Run("exact count complements survive display ceiling", func(t *testing.T) {
		for _, test := range []struct {
			predicate expr.Expr
			rows      float64
		}{
			{common, 14},
			{rare, 2},
			{comparison("<>", sqltype.CompareNotEqual, value(1)), 2},
			{LogicalExpr{Op: parser.LogicalNot, Left: common}, 2},
			{comparison("<", sqltype.CompareLess, value(2)), 14},
			{comparison("<=", sqltype.CompareLessEqual, value(2)), 16},
			{comparison("=", sqltype.CompareEqual, value(3)), 0},
		} {
			probability, known, err := EstimateSelectivity(context.Background(), test.predicate, inputs)
			display := (ExplainEstimate{Rows: probability * float64(stats.Rows)}).RoundedRows()
			if err != nil || !known || display != test.rows {
				t.Fatalf("probability=%v display=%v want=%v known=%v err=%v", probability, display, test.rows, known, err)
			}
		}
	})

	t.Run("residual unique and rank endpoints", func(t *testing.T) {
		unique := &storage.ColumnStatistics{Usage: integer.Usage(), Rows: 10, DistinctKnown: true, DistinctCount: 10}
		for i := int64(1); i <= 10; i++ {
			unique.Histogram = append(unique.Histogram, value(i))
		}
		inputs[0].Statistics = unique
		defer func() { inputs[0].Statistics = stats }()
		assert("unique equality", comparison("=", sqltype.CompareEqual, value(5)), .1, true)
		assert("rank strict", comparison("<", sqltype.CompareLess, value(5)), .4, true)
		assert("rank inclusive", comparison("<=", sqltype.CompareLessEqual, value(5)), .5, true)
		assert("below first", comparison("<", sqltype.CompareLess, value(1)), 0, true)
		assert("inclusive last", comparison("<=", sqltype.CompareLessEqual, value(10)), 1, true)
		unique.Rows, unique.DistinctCount = 1, 1
		unique.Histogram = []sqltype.Value{value(4)}
		assert("singleton strict", comparison("<", sqltype.CompareLess, value(4)), 0, true)
		assert("singleton inclusive", comparison("<=", sqltype.CompareLessEqual, value(4)), 1, true)
		unique.Rows = 3
		unique.Histogram = []sqltype.Value{value(4), value(4), value(4)}
		assert("duplicate boundaries", comparison(">", sqltype.CompareGreater, value(4)), 0, true)
		unique.DistinctKnown = false
		assert("unavailable not zero", comparison("=", sqltype.CompareEqual, value(4)), 0, false)
	})
	t.Run("cancel no estimation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := EstimateSelectivity(ctx, common, inputs)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel=%v", err)
		}
	})
	t.Run("finite arithmetic guard", func(t *testing.T) {
		for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -10, 10} {
			n := selectivityUnit(v)
			if math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 1 {
				t.Fatalf("not finite: %v", n)
			}
		}
	})
}

// Any accidental Expr.Eval call fails immediately rather than consulting a
// provider or running a query. Unsupported expressions must remain unavailable.
type selectivityUnevaluable struct{ expr.Info }

func (selectivityUnevaluable) Eval(expr.Context) (sqltype.Value, error) {
	panic("selectivity evaluated an expression")
}
