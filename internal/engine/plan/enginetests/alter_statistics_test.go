package enginetests

import (
	"context"
	"math"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestAlterStatisticsEstimates(t *testing.T) {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	integer, boolean := types[sqltype.Int4], types[sqltype.Bool]
	value, err := sqltype.Integer(integer, 1)
	if err != nil {
		t.Fatal(err)
	}
	column := plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage()}, Scope: 1, Frame: 1, Slot: 0, QueryOccurrence: 1}
	binding, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, integer, integer, boolean, integer.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	predicate := plan.ComparisonExpr{Info: expr.Info{Usage: boolean.Usage()}, Left: column, Right: plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: value}, Operator: "=", Predicate: binding}
	for _, tc := range []struct{ override, population, want, residual float64 }{
		// Residuals independently include the measured MCV ceiling 4/20.
		{0, 20, .05, 4.0 / 95}, {2, 20, .5, .2},
		{2.5, 20, .5, .2}, {3.5, 20, .25, .2}, {.25, 20, 1, .2},
		{-.5, 20, .1, 4.0 / 45}, {-.5, 40, .05, 4.0 / 95},
		{-.5, 5, .5, .2}, {-.5, 7, .25, .2},
		{-1, 40, .025, 4.0 / 195}, {math.MaxFloat64, 20, 1e-100, 8e-101},
	} {
		fact := &storage.ColumnStatistics{Usage: integer.Usage(), Rows: 20, DistinctCount: 20, DistinctKnown: true, DistinctOverride: tc.override}
		got, known, err := plan.EstimateSelectivity(context.Background(), predicate, []plan.ColumnStatisticInput{{Column: column, Statistics: fact, SourceRows: tc.population}})
		if err != nil || !known || math.Abs(got-tc.want) > tc.want*1e-12 || math.IsNaN(got) || math.IsInf(got, 0) || fact.DistinctCount != 20 {
			t.Fatal(tc, got, known, err, fact)
		}
		encoded, known := fact.EncodedDistinct()
		want := tc.override
		if want == 0 {
			want = -1
		}
		if !known || encoded != want {
			t.Fatal("encoding", encoded, want)
		}
		// An MCV hit remains a measured fraction even with an arbitrary estimate.
		fact.MCV = []storage.StatisticMCV{{Value: value, Count: 4}}
		got, known, err = plan.EstimateSelectivity(context.Background(), predicate, []plan.ColumnStatisticInput{{Column: column, Statistics: fact, SourceRows: tc.population}})
		if err != nil || !known || got != .2 || fact.MCV[0].Count != 4 {
			t.Fatal("override changed measured MCV mass", got, known, err)
		}
		unseen, err := sqltype.Integer(integer, 2)
		if err != nil {
			t.Fatal(err)
		}
		residual := predicate
		residual.Right = plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: unseen}
		got, known, err = plan.EstimateSelectivity(context.Background(), residual, []plan.ColumnStatisticInput{{Column: column, Statistics: fact, SourceRows: tc.population}})
		if err != nil || !known || math.Abs(got-tc.residual) > tc.residual*1e-12 {
			t.Fatal("residual omitted effective-count rounding or the measured MCV ceiling", tc, got, tc.residual, err)
		}
		notEqual, err := sqltype.BindComparisonPredicate(sqltype.CompareNotEqual, integer, integer, boolean, integer.Usage().Collation, false)
		if err != nil {
			t.Fatal(err)
		}
		residual.Operator, residual.Predicate = "<>", notEqual
		got, known, err = plan.EstimateSelectivity(context.Background(), residual, []plan.ColumnStatisticInput{{Column: column, Statistics: fact, SourceRows: tc.population}})
		if err != nil || !known || math.Abs(got-(1-tc.residual)) > 1e-12 {
			t.Fatal("inequality complemented before the measured MCV ceiling", tc, got, 1-tc.residual, err)
		}
	}
	// Two measured MCVs distinguish the least-common ceiling from the first
	// MCV. NULL mass must remain excluded from the subsequent <> complement.
	second, err := sqltype.Integer(integer, 2)
	if err != nil {
		t.Fatal(err)
	}
	unlisted, err := sqltype.Integer(integer, 3)
	if err != nil {
		t.Fatal(err)
	}
	notEqual, err := sqltype.BindComparisonPredicate(sqltype.CompareNotEqual, integer, integer, boolean, integer.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, override := range []float64{2, 2.5, -.1} {
		fact := &storage.ColumnStatistics{Usage: integer.Usage(), Rows: 20, NullCount: 2, DistinctCount: 14, DistinctKnown: true, DistinctOverride: override, MCV: []storage.StatisticMCV{{Value: value, Count: 4}, {Value: second, Count: 2}}}
		comparison := predicate
		comparison.Right = plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: unlisted}
		for _, negate := range []bool{false, true} {
			want := .1
			if negate {
				comparison.Operator, comparison.Predicate, want = "<>", notEqual, .8
			}
			got, known, err := plan.EstimateSelectivity(context.Background(), comparison, []plan.ColumnStatisticInput{{Column: column, Statistics: fact, SourceRows: 20}})
			if err != nil || !known || math.Abs(got-want) > 1e-12 || fact.DistinctCount != 14 || fact.MCV[1].Count != 2 {
				t.Fatal("least-common MCV ceiling/NULL-aware complement", override, negate, got, want, err)
			}
		}
	}
	// Target controls only bounded summaries, never raw population or distinctness.
	for _, target := range []int{1, 4} {
		input := []storage.StatisticsColumn{{Key: storage.StatisticsKey{Attribute: 1}, Usage: integer.Usage(), Slot: 0, Target: target}}
		stats, population, err := storage.CollectStatistics(context.Background(), input, func(yield func([]sqltype.Value) error) error {
			for n := int64(1); n <= 10; n++ {
				v, err := sqltype.Integer(integer, n)
				if err != nil {
					return err
				}
				if err := yield([]sqltype.Value{v}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		fact := stats.Columns[storage.StatisticsKey{Attribute: 1}]
		first, _ := fact.Histogram[0].Integer()
		last, _ := fact.Histogram[len(fact.Histogram)-1].Integer()
		if population != 10 || fact.DistinctCount != 10 || len(fact.Histogram) > target+1 || first != 1 || last != 10 {
			t.Fatal(target, population, fact)
		}
	}
	for _, target := range []int{1, 2} {
		input := []storage.StatisticsColumn{{Key: storage.StatisticsKey{Attribute: 1}, Usage: integer.Usage(), Slot: 0, Target: target}}
		stats, _, err := storage.CollectStatistics(context.Background(), input, func(yield func([]sqltype.Value) error) error {
			for group, count := range []int{4, 3, 2, 1} {
				for range count {
					v, err := sqltype.Integer(integer, int64(group+1))
					if err != nil {
						return err
					}
					if err := yield([]sqltype.Value{v}); err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		fact := stats.Columns[storage.StatisticsKey{Attribute: 1}]
		if fact.DistinctCount != 4 || len(fact.MCV) != target || fact.MCV[0].Count != 4 || target == 2 && fact.MCV[1].Count != 3 {
			t.Fatal("target changed real counts or failed MCV bound", target, fact)
		}
	}
}
